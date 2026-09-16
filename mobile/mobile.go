// Package mobile 是移动端（Android/iOS）内嵌 Go hub 的绑定入口。
//
// 去中心化迭代第 4 步：把 pkg/hubserver 编进移动端——App 启动时进程内
// 起一个回环嵌入式 hub（127.0.0.1 随机端口 + mesh 组网 + mDNS），Dart
// 侧直接连本地 ws 地址，安卓/苹果变成局域网里平等的一个 mesh 节点，
// 不再依赖「外部先跑 hub 进程」。
//
// 绑定方式：gomobile bind 导出本包（函数签名只含 string/error 等
// 跨语言安全类型）；Dart 经 FFI/JNI 调 Start/Stop。包内单实例：
// 一个 App 进程只起一个嵌入式 hub，Stop 幂等。
package mobile

import (
	"context"
	"sync"

	"github.com/pandaymx/lanchat/pkg/hubserver"
)

var (
	mu   sync.Mutex
	hub  *hubserver.Server
	stop context.CancelFunc
)

// Start 起本进程内的嵌入式 hub（127.0.0.1 随机端口 + mesh + mDNS），
// 返回本地 ws 地址（如 ws://127.0.0.1:34123/ws）。dataDir 是数据目录
// （Android 传应用私有 files 目录；空用平台默认）；nodeID 是 mesh
// 节点标识（建议用持久化设备 ID，空用主机名）；version 进日志/mDNS。
// 已启动时幂等返回现有地址。
func Start(dataDir, nodeID, version string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	if hub != nil {
		return "ws://" + hub.Addr() + "/ws", nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv, ws, err := hubserver.StartEmbedded(ctx, hubserver.Config{
		DataDir: dataDir,
		NodeID:  nodeID,
		Version: version,
	})
	if err != nil {
		cancel()
		return "", err
	}
	hub, stop = srv, cancel
	return ws, nil
}

// Stop 停掉嵌入式 hub（幂等；重复调用无副作用）。
func Stop() {
	mu.Lock()
	defer mu.Unlock()
	if hub != nil {
		_ = hub.Close()
		stop()
		hub, stop = nil, nil
	}
}

// Addr 返回当前嵌入式 hub 的 ws 地址；未启动返回空串。
func Addr() string {
	mu.Lock()
	defer mu.Unlock()
	if hub == nil {
		return ""
	}
	return "ws://" + hub.Addr() + "/ws"
}
