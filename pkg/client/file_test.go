package client_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/pkg/client"
	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

func TestHTTPBaseFromWS(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ws://127.0.0.1:9000/ws", "http://127.0.0.1:9000"},
		{"127.0.0.1:9000", "http://127.0.0.1:9000"},
		{"ws://host:9000/ws", "http://host:9000"},
		{"wss://hub.example.com/ws?x=1", "https://hub.example.com"},
	}
	for _, c := range cases {
		if got := client.HTTPBaseFromWS(c.in); got != c.want {
			t.Errorf("HTTPBaseFromWS(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// uploadServer 模拟 hub 的 /api/files 上传端点（与 hubapi 行为一致）。
func uploadServer(t *testing.T, content []byte) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/files":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				http.Error(w, "bad multipart", http.StatusBadRequest)
				return
			}
			f, h, err := r.FormFile("file")
			if err != nil {
				http.Error(w, "no file", http.StatusBadRequest)
				return
			}
			defer f.Close()
			got, _ := io.ReadAll(f)
			if !bytes.Equal(got, content) {
				http.Error(w, "content mismatch", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"fid":"` + strings.Repeat("a", 32) + `","n":"` + h.Filename + `","sz":` +
				itoa(len(content)) + `,"m":"text/plain"}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/files/"):
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write(content)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestUploadFile_RoundTrip(t *testing.T) {
	tr, _, store := newTransportWithHub(t, "memory://lanchat-test")
	hello := protocol.Hello{ProtocolVersion: protocol.ProtocolVersion, DeviceID: "d1", UserID: "alice"}
	c := newClient(t, tr, store, hello, 0)

	content := []byte("payload-123")
	ts := uploadServer(t, content)
	c.SetFileBase(ts.URL)

	// 上传 + 发消息：hub 回环后本地事件流出现带 FileRef 的文件消息。
	sub := c.Subscribe(8)
	defer sub.Close()

	src := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := c.UploadFile(context.Background(), "lobby", src, ""); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}

	ev := waitForEvent(t, sub, core.EventMessage, 2*time.Second)
	if ev == nil {
		t.Fatal("no EventMessage after UploadFile")
	}
	m := ev.Message
	if m.File == nil {
		t.Fatal("message has no File ref")
	}
	if m.File.FileID != strings.Repeat("a", 32) || m.File.Name != "notes.txt" || m.File.Size != int64(len(content)) {
		t.Errorf("File = %+v", m.File)
	}
	if m.SenderUserID != "alice" {
		t.Errorf("SenderUserID = %q", m.SenderUserID)
	}

	// 下载：内容一致。
	dest := filepath.Join(t.TempDir(), "out", "notes.txt")
	if err := c.DownloadFile(context.Background(), m.File.FileID, dest); err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("downloaded = %q, want %q", got, content)
	}

	// hub 侧落库：历史里也能查到文件消息（协议 JSON 往返保留了 FileRef）。
	hist, err := store.History(context.Background(), "lobby", 0, 10)
	if err != nil {
		t.Fatalf("hub history: %v", err)
	}
	if len(hist) != 1 || hist[0].File == nil || hist[0].File.Name != "notes.txt" {
		t.Errorf("hub history = %+v", hist)
	}
}

func TestDownloadFile_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)

	c := client.New(protocol.Hello{}, nil, nil, nil)
	c.SetFileBase(ts.URL)
	err := c.DownloadFile(context.Background(), strings.Repeat("b", 32), filepath.Join(t.TempDir(), "x"))
	if !errors.Is(err, core.ErrNotFound) {
		t.Errorf("DownloadFile(404) = %v, want ErrNotFound", err)
	}
}

func TestFileAPI_Unconfigured(t *testing.T) {
	c := client.New(protocol.Hello{}, nil, nil, nil)
	if err := c.UploadFile(context.Background(), "lobby", "/nonexistent", ""); !errors.Is(err, client.ErrFileUnconfigured) {
		t.Errorf("UploadFile without base = %v, want ErrFileUnconfigured", err)
	}
	if err := c.DownloadFile(context.Background(), "x", "/tmp/x"); !errors.Is(err, client.ErrFileUnconfigured) {
		t.Errorf("DownloadFile without base = %v, want ErrFileUnconfigured", err)
	}
}
