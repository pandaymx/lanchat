package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/internal/config"
	"github.com/pandaymx/lanchat/internal/server"
)

func TestRunCLISessionCommands(t *testing.T) {
	hash, err := config.HashPSK("cli-psk")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.New(server.Options{
		Listen:            "127.0.0.1:0",
		Path:              "/lctp",
		AuthMode:          config.AuthModePSK,
		PSKHash:           hash,
		HeartbeatInterval: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		ShutdownGrace:     time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	<-srv.Ready()

	wsURL := "ws://" + srv.ListenAddr() + "/lctp"
	in := strings.NewReader("/help\n/peers\n/status\n/fingerprint\n你好全体成员\n/quit\n")
	var out bytes.Buffer

	if err := runCLISession(ctx, in, &out, cliOptions{
		addr:        wsURL,
		psk:         "cli-psk",
		nick:        "TermUser",
		downloadDir: t.TempDir(),
	}); err != nil {
		t.Fatalf("runCLISession: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "可用命令:") || !strings.Contains(got, "status:conn=connected") || !strings.Contains(got, "fp:") {
		t.Fatalf("unexpected CLI output:\n%s", got)
	}
}
