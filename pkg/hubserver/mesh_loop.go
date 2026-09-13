package hubserver

// mesh_loop.go 是 hubserver 的 mesh 后台同步循环（ADR-014 wire v2）。
//
// 邻居集合 = 显式 MeshPeers ∪ mDNS 发现的 mesh 实例（meta mesh=1）。
// 每个周期对每个邻居跑一轮 mesh.PullOnce：本地游标 → 远端增量 →
// 幂等落库。失败只记日志，不中断循环。

import (
	"context"
	"net"
	"strconv"
	"time"

	"github.com/pandaymx/lanchat/internal/discovery"
	"github.com/pandaymx/lanchat/pkg/mesh"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// meshSyncInterval 是邻居同步周期。局域网规模下 5s 足够收敛，
// 又不至于在空闲时制造过多流量。
const meshSyncInterval = 5 * time.Second

// httpRemote 把 base URL 适配成 mesh.Remote（HTTP POST 一轮同步）。
type httpRemote struct {
	baseURL string
}

// Sync 实现 mesh.Remote：把请求发往远端 mesh 端点。
func (r httpRemote) Sync(ctx context.Context, req protocol.SyncRequest) ([]protocol.SyncResponse, error) {
	return mesh.SyncPeer(ctx, r.baseURL, req)
}

// meshLoop 周期性向邻居同步。peerURLs 是显式邻居；mDNS 发现的
// mesh 实例在启动时并入，后续周期不刷新（邻居列表变化下一版做）。
func (s *Server) meshLoop(ctx context.Context, peerURLs []string) {
	peers := map[string]bool{}
	for _, u := range peerURLs {
		peers[u] = true
	}
	// 局域网发现：收集带 mesh=1 的实例，转成 base URL。
	if s.cfg.MDNS {
		instances, err := discovery.Discover(ctx, 2*time.Second)
		if err == nil {
			for _, in := range instances {
				url := "http://" + net.JoinHostPort(in.Addr, strconv.Itoa(in.Port))
				peers[url] = true
			}
			s.logger.Info("mesh discovered peers", "count", len(peers))
		} else {
			s.logger.Warn("mesh discovery failed", "err", err)
		}
	}
	if len(peers) == 0 {
		s.logger.Info("mesh loop: no peers")
		return
	}

	s.logger.Info("mesh loop started", "peers", len(peers), "node", s.nodeID)
	ticker := time.NewTicker(meshSyncInterval)
	defer ticker.Stop()

	syncAll := func() {
		for u := range peers {
			if err := s.syncOnce(ctx, u); err != nil {
				s.logger.Warn("mesh sync failed", "peer", u, "err", err)
			}
		}
	}

	// 启动立即同步一轮（收敛快），随后周期续。
	syncAll()
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("mesh loop stopped")
			return
		case <-ticker.C:
			syncAll()
		}
	}
}

// syncOnce 对单个邻居执行一轮 PullOnce。
func (s *Server) syncOnce(ctx context.Context, peerURL string) error {
	n, err := mesh.PullOnce(ctx, s.meshStore, httpRemote{baseURL: peerURL})
	if err != nil {
		return err
	}
	if n > 0 {
		s.logger.Info("mesh pulled", "peer", peerURL, "messages", n)
	}
	return nil
}
