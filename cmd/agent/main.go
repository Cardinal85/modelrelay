// agent 是 ModelRelay 的内网连接程序：
// 主动连接 Relay，透明转发本地 OpenAI-compatible 模型服务的请求与响应。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"modelrelay/internal/agent"
	"modelrelay/internal/config"
	"modelrelay/internal/version"
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	configPath := flag.String("config", "", "path to agent config file (yaml)")
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s agent %s (go %s)\n", version.Name, version.AgentVersion(), version.GoVersion())
		return
	}
	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "agent: missing -config path")
		flag.Usage()
		os.Exit(2)
	}

	cfg := config.DefaultAgent()
	if err := config.LoadFile(*configPath, cfg); err != nil {
		log.Fatalf("agent: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := agent.New(cfg)
	if err != nil {
		log.Fatalf("agent: %v", err)
	}
	log.Printf("%s agent %s started: node=%s relays=%d local=%s",
		version.Name, version.AgentVersion(), cfg.NodeID, len(cfg.Relays), cfg.Local.BaseURL)

	a.Run(ctx)
}
