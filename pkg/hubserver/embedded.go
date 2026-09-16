// Package hubserver —— 嵌入式 hub 的「本进程专用」启动辅助。
//
// 去中心化迭代（删除 hub 依赖）：各端（desktop / tui / web）不再要求
// 「先另跑一个独立 hub 进程」，而是进程内起一个回环嵌入式 hub，配
// mesh 组网 + mDNS 广播/发现，本端直接连回自己。StartEmbedded 是
// 三端共用的统一入口（桌面端 webapp 与 cmd/tui、cmd/web 同款装配）。
package hubserver

import (
	"context"

	wstransport "github.com/pandaymx/lanchat/pkg/transport/ws"
)

// StartEmbedded 起一个本进程专用的嵌入式 hub 并返回其 ws 地址。
//
// cfg 只取 DataDir/DBPath/FilesDir/MDNS/NodeID/MeshPeers/Version 作
// 覆盖；Addr 固定 127.0.0.1 随机端口、Mesh 固定 true（本进程语义），
// 传入值忽略。ctx 由调用方持有并负责 cancel（Server.Close 是空操作，
// 关停靠 ctx 取消），故返回 *Server 让调用方可查 Addr()/做测试断言。
func StartEmbedded(ctx context.Context, cfg Config) (*Server, string, error) {
	embCfg := Config{
		Addr:    "127.0.0.1:0",
		Mesh:    true,
		Version: cfg.Version,
		// Logger 留空：hubserver 自建 "hub" 组件，日志独立可辨。
	}
	embCfg.DataDir = cfg.DataDir
	embCfg.MeshDir = cfg.MeshDir
	embCfg.DBPath = cfg.DBPath
	embCfg.FilesDir = cfg.FilesDir
	embCfg.MDNS = cfg.MDNS
	embCfg.NodeID = cfg.NodeID
	embCfg.Device = cfg.Device
	embCfg.JoinToken = cfg.JoinToken
	embCfg.MeshPeers = cfg.MeshPeers

	srv, err := Start(ctx, embCfg)
	if err != nil {
		return nil, "", err
	}
	return srv, "ws://" + srv.Addr() + wstransport.DefaultPath, nil
}
