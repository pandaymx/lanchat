package discover

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestGossipPeerBookAndProbeServers(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	bookPath := filepath.Join(t.TempDir(), "gossip.json")
	book := NewGossipPeerBook(bookPath)
	if n := book.Add(ln.Addr().String(), "127.0.0.1:1"); n != 2 {
		t.Fatalf("Add returned %d, want 2", n)
	}

	reloaded := NewGossipPeerBook(bookPath)
	eps := reloaded.Endpoints()
	if len(eps) != 2 {
		t.Fatalf("Endpoints len = %d, want 2", len(eps))
	}

	found := ProbeServers(context.Background(), eps, 7878, 300*time.Millisecond)
	if len(found) != 1 {
		t.Fatalf("ProbeServers found %d servers, want 1", len(found))
	}
}
