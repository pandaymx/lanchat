package hubapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"path/filepath"
	"testing"

	"github.com/pandaymx/lanchat/internal/hubapi"
	"github.com/pandaymx/lanchat/pkg/hubfile"
	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/store/memory"
)

// newTestServer 起一个 httptest server，挂上传/下载端点。
func newTestServer(t *testing.T, maxSize int64) (*httptest.Server, *hubfile.Service) {
	t.Helper()
	store := memory.New()
	svc, err := hubfile.New(filepath.Join(t.TempDir(), "files"), store, maxSize)
	if err != nil {
		t.Fatalf("hubfile.New: %v", err)
	}
	mux := http.NewServeMux()
	hubapi.NewFilesAPI(svc).Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, svc
}

func upload(t *testing.T, url, name, contentType string, body []byte) (*http.Response, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	// CreatePart + 手动 Content-Disposition：能同时设置 part 的
	// Content-Type（CreateFormFile 不设，hub 侧会回落 octet-stream，
	// 与真实浏览器上传行为一致）。
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, name))
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	fw, err := mw.CreatePart(h)
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	if _, err := fw.Write(body); err != nil {
		t.Fatalf("write body: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, url, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload request: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func TestFilesAPI_UploadDownload_RoundTrip(t *testing.T) {
	ts, _ := newTestServer(t, 0)

	content := []byte("#!/bin/bash\necho hello\n")
	resp, out := upload(t, ts.URL+"/api/files", "script.sh", "text/x-sh", content)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d, body=%v", resp.StatusCode, out)
	}
	fid, _ := out["fid"].(string)
	if len(fid) != 32 {
		t.Fatalf("fid = %q, want 32 hex", fid)
	}
	if out["n"] != "script.sh" || int64(out["sz"].(float64)) != int64(len(content)) || out["m"] != "text/x-sh" {
		t.Errorf("upload response = %v", out)
	}

	// 下载：内容与 Content-Disposition 一致。
	dresp, err := http.Get(ts.URL + "/api/files/" + fid)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer dresp.Body.Close()
	got, _ := io.ReadAll(dresp.Body)
	if !bytes.Equal(got, content) {
		t.Errorf("downloaded = %q, want %q", got, content)
	}
	if dresp.Header.Get("Content-Type") != "text/x-sh" {
		t.Errorf("Content-Type = %q", dresp.Header.Get("Content-Type"))
	}
	if cd := dresp.Header.Get("Content-Disposition"); !bytes.Contains([]byte(cd), []byte("script.sh")) {
		t.Errorf("Content-Disposition = %q, want filename script.sh", cd)
	}
}

func TestFilesAPI_Download_Range(t *testing.T) {
	ts, _ := newTestServer(t, 0)
	content := []byte("0123456789abcdef")
	resp, out := upload(t, ts.URL+"/api/files", "r.bin", "", content)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d", resp.StatusCode)
	}
	fid, _ := out["fid"].(string)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/files/"+fid, nil)
	req.Header.Set("Range", "bytes=2-5")
	rresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("range request: %v", err)
	}
	defer rresp.Body.Close()
	if rresp.StatusCode != http.StatusPartialContent {
		t.Fatalf("range status = %d, want 206", rresp.StatusCode)
	}
	part, _ := io.ReadAll(rresp.Body)
	if string(part) != "2345" {
		t.Errorf("range body = %q, want 2345", part)
	}
}

func TestFilesAPI_Upload_TooLarge(t *testing.T) {
	ts, _ := newTestServer(t, 4)
	resp, out := upload(t, ts.URL+"/api/files", "big.bin", "", []byte("12345"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize upload status = %d (body=%v), want 413", resp.StatusCode, out)
	}
	// 超限后目录应为空（hubfile 已清理）。
	// 目录内容在 hubfile 层测过，这里只验证状态码路径。
}

func TestFilesAPI_Upload_MissingField(t *testing.T) {
	ts, _ := newTestServer(t, 0)
	// 非 multipart 直接 POST：应 400。
	resp, err := http.Post(ts.URL+"/api/files", "text/plain", bytes.NewBufferString("x"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad upload status = %d, want 400", resp.StatusCode)
	}
}

func TestFilesAPI_Download_NotFound(t *testing.T) {
	ts, _ := newTestServer(t, 0)
	// 非法形态 ID：404（不泄露存储布局）。
	for _, id := range []string{"..%2f..%2fetc%2fpasswd", "abc", "ZZZZ0000000000000000000000000000"} {
		resp, err := http.Get(ts.URL + "/api/files/" + id)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET /api/files/%s = %d, want 404", id, resp.StatusCode)
		}
	}
}

// 编译期断言：memory.Store 满足 hubfile.MetaStore（cmd/hub 里直接把
// core.Store 传给 hubfile.New）。
var (
	_ hubfile.MetaStore = (*memory.MemoryStore)(nil)
	_                   = protocol.FileRef{}
)
