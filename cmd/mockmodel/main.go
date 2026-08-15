// mockmodel 是一个模拟的 OpenAI-compatible 模型服务，用于本地联调与测试。
//
// 用法:
//
//	mockmodel [-listen :8000] [-models qwen-local,llama-3] [-echo]
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"

	"modelrelay/internal/testutil"
	"modelrelay/internal/version"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8000", "listen address")
	models := flag.String("models", "mock-model", "comma separated model ids")
	echo := flag.Bool("echo", true, "echo last user message in chat responses")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s mockmodel %s\n", version.Name, version.AgentVersion())
		return
	}

	m := testutil.NewMockUpstream(strings.Split(*models, ",")...)
	m.EchoMode = *echo

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("mockmodel: listen %s: %v", *listen, err)
	}
	log.Printf("%s mockmodel started at http://%s/v1 (models: %s)",
		version.Name, ln.Addr(), strings.Join(strings.Split(*models, ","), ", "))
	if err := http.Serve(ln, m.Server.Config.Handler); err != nil {
		log.Fatalf("mockmodel: %v", err)
	}
}
