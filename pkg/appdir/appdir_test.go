package appdir

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// 用可注入的基准路径测分支逻辑：各平台规则在 GOOS 分支里，
// 当前平台下验证 env 优先级与 fallback 行为即可。

func TestDataDirWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only rule")
	}
	t.Setenv("LOCALAPPDATA", `C:\Users\tester\AppData\Local`)
	got := DataDir("LAN Chat Hub")
	want := `C:\Users\tester\AppData\Local\LAN Chat Hub`
	if got != want {
		t.Fatalf("DataDir = %q, want %q", got, want)
	}
}

func TestDataDirLinuxXDG(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("not windows")
	}
	t.Setenv("XDG_DATA_HOME", "/home/tester/.local/share")
	t.Setenv("HOME", "/home/tester")
	got := DataDir("lanchat")
	want := filepath.Join("/home/tester/.local/share", "lanchat")
	if got != want {
		t.Fatalf("DataDir = %q, want %q", got, want)
	}
}

func TestDataDirLinuxHomeFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("not windows")
	}
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "/home/tester")
	got := DataDir("lanchat")
	want := filepath.Join("/home/tester", ".local", "share", "lanchat")
	if got != want {
		t.Fatalf("DataDir = %q, want %q", got, want)
	}
}

func TestDataDirAlwaysNonEmpty(t *testing.T) {
	got := DataDir("lanchat")
	if strings.TrimSpace(got) == "" {
		t.Fatal("DataDir returned empty path")
	}
}

func TestDownloadDirEnvOverride(t *testing.T) {
	t.Setenv("LANCHAT_DOWNLOAD_DIR", "/tmp/custom-dl")
	got := DownloadDir("lanchat")
	if got != "/tmp/custom-dl" {
		t.Fatalf("DownloadDir = %q, want /tmp/custom-dl", got)
	}
}

func TestDownloadDirAlwaysNonEmpty(t *testing.T) {
	got := DownloadDir("lanchat")
	if strings.TrimSpace(got) == "" {
		t.Fatal("DownloadDir returned empty path")
	}
}

func TestAppName(t *testing.T) {
	name := AppName()
	if strings.TrimSpace(name) == "" {
		t.Fatal("AppName empty")
	}
}
