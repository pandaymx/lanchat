package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/config"
	"github.com/pandaymx/lanchat/internal/core"
	"github.com/pandaymx/lanchat/internal/server"
)

func newTUITestClient(t *testing.T, wsURL, psk, nick string, listener appapi.Listener) *core.Client {
	t.Helper()
	cli := core.New(core.Options{
		Nickname:    nick,
		OS:          "linux",
		DownloadDir: t.TempDir(),
		Listener:    listener,
	})
	t.Cleanup(cli.Close)
	if err := cli.Connect(wsURL, psk); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	return cli
}

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
	in := strings.NewReader("/help\n/peers\n/status\n/fingerprint\n你好全体成员\n/recall\n/reedit 重新编辑后的消息\n/quit\n")
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
	if !strings.Contains(got, "可用命令:") || !strings.Contains(got, "status:conn=connected") || !strings.Contains(got, "fp:") || !strings.Contains(got, "[重编辑已发送] 重新编辑后的消息") {
		t.Fatalf("unexpected CLI output:\n%s", got)
	}
}

func TestTUIModelRenderAndInteraction(t *testing.T) {
	parsed := parseTUIMessageParts("「下午开会讨论发版」\n收到，马上准备 [👍] [❤️] [👍]")
	if parsed.QuoteText != "下午开会讨论发版" || parsed.BodyText != "收到，马上准备" || len(parsed.Reactions) != 2 || parsed.Reactions[0].Count != 2 {
		t.Fatalf("unexpected parseTUIMessageParts: %+v", parsed)
	}

	hash, err := config.HashPSK("tui-psk")
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
	listener := &tuiEventListener{}
	cli := newTUITestClient(t, wsURL, "tui-psk", "TUIUser", listener)
	model := newTUIModel(cli, t.TempDir())
	listener.setModel(model)

	// 模拟键盘输入中文消息并回车发送
	handleTUIKeyBytes(model, []byte("全屏TUI测试消息\n"))
	handleTUIKeyBytes(model, []byte("/react 👍\n"))

	frame := model.renderFrame(100, 28)
	if !strings.Contains(frame, "LANChat TUI") || !strings.Contains(frame, "全屏TUI测试消息") || !strings.Contains(frame, "[ 👍 1 ]") {
		t.Fatalf("unexpected TUI frame before recall:\n%s", frame)
	}

	// 模拟 /recall 撤回与 /reedit 回填输入框
	handleTUIKeyBytes(model, []byte("/recall\n"))
	frameAfterRecall := model.renderFrame(100, 28)
	if !strings.Contains(frameAfterRecall, "你撤回了一条消息 (输入 /reedit 重新编辑)") {
		t.Fatalf("expected recall pill in TUI frame, got:\n%s", frameAfterRecall)
	}

	handleTUIKeyBytes(model, []byte("/reedit\n"))
	if gotInput := string(model.inputBuf); gotInput != "全屏TUI测试消息" {
		t.Fatalf("expected /reedit to prefill inputBuf with %q, got %q", "全屏TUI测试消息", gotInput)
	}
}
