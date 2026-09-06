package webui

import (
	"context"
	"errors"
	"fmt"

	"github.com/pandaymx/lanchat/pkg/client"
	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/event"
	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/store/memory"
)

// DialOptions 是 DialClient 的入参。Transport 必填，其余有兜底。
type DialOptions struct {
	// Transport 决定底层连接如何建立：生产用 pkg/transport/ws，
	// 测试用 pkg/transport/fake。这就是 ADR-002 的可替换点。
	Transport core.Transport
	// HubURL 形如 ws://127.0.0.1:9000（ws 实现会把 path 规范化成 /ws）。
	HubURL string
	// User / Device 组成 ADR-008 的二层身份；Device 同时是 ReadCursor 主键。
	User   string
	Device string
	// HistoryLimit 是首屏历史拉取条数，<=0 用 client 默认值（50）。
	HistoryLimit int
}

// DialClient 建立到 hub 的连接并完成握手（Hello + 历史补发）。
//
// 装配模式仿 pkg/tui.Dial：transport.Dial → memory store → event bus →
// client.New → Connect(RequestHistory)。web 端不内嵌 hub 逻辑，只作为一个
// 普通客户端接 hub（提案 §3.2 thin proxy）。
//
// 成功返回的 client 与 store 由调用方释放，顺序必须是先 cli.Close() 再
// store.Close()：先停 readPump 再关 store，避免往已关闭的 store 里写。
// Dial 成功但 Connect 失败时，conn 与 store 已在函数内回收，无需调用方处理。
func DialClient(ctx context.Context, opts DialOptions) (*client.Client, core.Store, error) {
	if opts.Transport == nil {
		return nil, nil, errors.New("webui: DialOptions.Transport is required")
	}

	hello := protocol.Hello{
		ProtocolVersion: protocol.ProtocolVersion,
		DeviceID:        opts.Device,
		UserID:          opts.User,
	}

	conn, err := opts.Transport.Dial(ctx, opts.HubURL, hello)
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", opts.HubURL, err)
	}

	store := memory.New()
	bus := event.New()
	cli := client.New(hello, conn, store, bus)

	if err := cli.Connect(ctx, client.ConnectOptions{
		RequestHistory: true,
		HistoryLimit:   opts.HistoryLimit,
	}); err != nil {
		_ = conn.Close()
		_ = store.Close()
		return nil, nil, fmt.Errorf("connect: %w", err)
	}
	return cli, store, nil
}
