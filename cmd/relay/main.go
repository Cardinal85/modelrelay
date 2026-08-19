// relay 是 ModelRelay 的中转与调度服务：
// 面向 New API 提供 OpenAI-compatible HTTP 上游，通过 WSS/mTLS 管理 Agent 节点。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"modelrelay/internal/config"
	"modelrelay/internal/relay"
	"modelrelay/internal/store"
	"modelrelay/internal/version"
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	configPath := flag.String("config", "", "path to relay config file (yaml)")
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s relay %s (go %s)\n", version.Name, version.RelayVersion(), version.GoVersion())
		return
	}
	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "relay: missing -config path")
		flag.Usage()
		os.Exit(2)
	}

	cfg := config.DefaultRelay()
	if err := config.LoadFile(*configPath, cfg); err != nil {
		log.Fatalf("relay: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg); err != nil {
		log.Fatalf("relay: %v", err)
	}
}

func run(ctx context.Context, cfg *config.Relay) error {
	srv := relay.NewServer(&relay.RelayConfig{
		RelayID:             cfg.RelayID,
		HTTPListen:          cfg.HTTPListen,
		WSSListen:           cfg.WSSListen,
		MaxBodyBytes:        cfg.Limits.MaxBodyBytes,
		MaxConcurrency:      cfg.Limits.MaxConcurrency,
		QueueLength:         cfg.Limits.QueueLength,
		QueueTimeoutMs:      int64(cfg.Limits.QueueTimeoutSec) * 1000,
		TTFTTimeoutMs:       int64(cfg.Limits.TTFTTimeoutSec) * 1000,
		IdleTimeoutMs:       int64(cfg.Limits.IdleTimeoutSec) * 1000,
		RequestTimeoutMs:    int64(cfg.Limits.RequestTimeoutSec) * 1000,
		HeartbeatTimeoutS:   cfg.Limits.HeartbeatTimeoutSec,
		InternalAuthToken:   cfg.InternalAuth.Token,
		InternalAuthEnabled: cfg.InternalAuth.Enabled,
	})
	srv.SetRetention(cfg.Retention.KeepPromptResponse, cfg.Retention.RetentionDays)

	// SQLite 持久化 + 初始管理员。
	var st *store.Store
	if cfg.Store.DBPath != "" {
		var err error
		st, err = store.Open(cfg.Store.DBPath)
		if err != nil {
			return err
		}
		defer st.Close()
		srv.SetStore(st)
		for _, u := range cfg.Admin.Users {
			if u.Username == "" || u.Password == "" {
				continue
			}
			role := u.Role
			if role != "admin" && role != "readonly" {
				role = "readonly"
			}
			if err := st.EnsureAdmin(u.Username, u.Password, role); err != nil {
				return fmt.Errorf("relay: bootstrap admin user %s: %w", u.Username, err)
			}
		}
		log.Printf("relay: store ready at %s (schema v%d)", cfg.Store.DBPath, mustStoreVersion(st))
	}

	wss, err := relay.NewWSSServer(srv, cfg.TLSCert, cfg.TLSKey, cfg.AgentCA, cfg.WSSListen)
	if err != nil {
		return err
	}
	wss.SetStore(st)
	upstream, err := relay.NewUpstreamServer(srv, cfg.HTTPListen)
	if err != nil {
		return err
	}

	errCh := make(chan error, 3)
	go func() { errCh <- wss.Start() }()
	go func() { errCh <- upstream.Start() }()

	var admin *relay.AdminServer
	if cfg.Admin.Listen != "" {
		ttl := cfg.Admin.SessionTimeout
		if ttl <= 0 {
			ttl = time.Duration(cfg.Admin.SessionTTLMin) * time.Minute
		}
		admin, err = relay.NewAdminServerWithOptions(srv, st, relay.AdminOptions{
			Listen:          cfg.Admin.Listen,
			SessionTTL:      ttl,
			TrustedProxies:  cfg.Admin.TrustedProxies,
			SecureCookie:    cfg.Admin.SecureCookie,
			TurnstileSite:   cfg.Admin.Turnstile.SiteKey,
			TurnstileSecret: cfg.Admin.Turnstile.SecretKey,
		})
		if err != nil {
			return err
		}
		go func() { errCh <- admin.Start() }()
	}

	log.Printf("%s relay %s started: http=%s wss=%s admin=%s",
		version.Name, version.RelayVersion(), upstream.Addr(), wss.Addr(), adminAddr(admin))

	select {
	case <-ctx.Done():
		log.Printf("relay: shutting down...")
		shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = upstream.Close(shCtx)
		_ = wss.Close(shCtx)
		if admin != nil {
			_ = admin.Close()
		}
		srv.Close()
		return nil
	case err := <-errCh:
		return err
	}
}

func mustStoreVersion(st *store.Store) int {
	v, _ := st.Version()
	return v
}

func adminAddr(a *relay.AdminServer) string {
	if a == nil {
		return "disabled"
	}
	return a.Addr().String()
}
