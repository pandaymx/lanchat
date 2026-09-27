package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// withEnv 设置环境变量并在测试结束后还原。
func withEnv(t *testing.T, k, v string) {
	t.Helper()
	old, ok := os.LookupEnv(k)
	if err := os.Setenv(k, v); err != nil {
		t.Fatalf("设置环境变量 %s: %v", k, err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(k, old)
		} else {
			_ = os.Unsetenv(k)
		}
	})
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"LANCHAT_LISTEN", "LANCHAT_PATH", "LANCHAT_PSK_HASH",
		"LANCHAT_AUTH_MODE", "LANCHAT_HEARTBEAT_SEC",
		"LANCHAT_IDLE_TIMEOUT_SEC", "LANCHAT_SHUTDOWN_GRACE_SEC",
	} {
		_, ok := os.LookupEnv(k)
		if ok {
			_ = os.Unsetenv(k)
			t.Cleanup(func(k string) func() {
				return func() { _ = os.Unsetenv(k) }
			}(k))
		}
	}
}

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.Server.Listen != DefaultListen {
		t.Errorf("Listen = %q, 期望 %q", cfg.Server.Listen, DefaultListen)
	}
	if cfg.Server.Path != DefaultPath {
		t.Errorf("Path = %q, 期望 %q", cfg.Server.Path, DefaultPath)
	}
	if cfg.Server.AuthMode != AuthModePSK {
		t.Errorf("AuthMode = %q, 期望 %q", cfg.Server.AuthMode, AuthModePSK)
	}
	if cfg.Server.HeartbeatSec != DefaultHeartbeatSec {
		t.Errorf("HeartbeatSec = %d, 期望 %d", cfg.Server.HeartbeatSec, DefaultHeartbeatSec)
	}
	if cfg.Server.IdleTimeoutSec != DefaultIdleTimeoutSec {
		t.Errorf("IdleTimeoutSec = %d, 期望 %d", cfg.Server.IdleTimeoutSec, DefaultIdleTimeoutSec)
	}
	if cfg.Server.ShutdownGraceSec != DefaultShutdownGraceSec {
		t.Errorf("ShutdownGraceSec = %d, 期望 %d", cfg.Server.ShutdownGraceSec, DefaultShutdownGraceSec)
	}
}

func TestLoadRequiresPSKHashByDefault(t *testing.T) {
	clearConfigEnv(t)
	if _, err := Load("", nil); err == nil {
		t.Fatal("authMode=psk 且缺 pskHash 时应报错，实际通过")
	}
}

func TestLoadAuthModeNone(t *testing.T) {
	clearConfigEnv(t)
	cfg, err := Load("", map[string]string{"auth-mode": AuthModeNone})
	if err != nil {
		t.Fatalf("authMode=none 应通过，得到错误: %v", err)
	}
	if cfg.Server.AuthMode != AuthModeNone {
		t.Errorf("AuthMode = %q, 期望 none", cfg.Server.AuthMode)
	}
}

func TestLoadInvalidAuthMode(t *testing.T) {
	clearConfigEnv(t)
	if _, err := Load("", map[string]string{"auth-mode": "bogus"}); err == nil {
		t.Fatal("非法 authMode 应报错，实际通过")
	}
}

func TestLoadEnvOverridesAndPSKHash(t *testing.T) {
	clearConfigEnv(t)
	hash, err := HashPSK("env-secret")
	if err != nil {
		t.Fatalf("HashPSK: %v", err)
	}
	withEnv(t, "LANCHAT_PSK_HASH", hash)
	withEnv(t, "LANCHAT_LISTEN", ":20000")
	withEnv(t, "LANCHAT_HEARTBEAT_SEC", "7")

	cfg, err := Load("", nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Listen != ":20000" {
		t.Errorf("Listen = %q, 期望 :20000", cfg.Server.Listen)
	}
	if cfg.Server.PSKHash != hash {
		t.Errorf("PSKHash 未被环境变量覆盖")
	}
	if cfg.Server.HeartbeatSec != 7 {
		t.Errorf("HeartbeatSec = %d, 期望 7", cfg.Server.HeartbeatSec)
	}
}

func TestLoadFlagBeatsEnv(t *testing.T) {
	clearConfigEnv(t)
	hash, err := HashPSK("secret")
	if err != nil {
		t.Fatalf("HashPSK: %v", err)
	}
	withEnv(t, "LANCHAT_PSK_HASH", hash)
	withEnv(t, "LANCHAT_LISTEN", ":20000")

	cfg, err := Load("", map[string]string{"listen": ":21000"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Listen != ":21000" {
		t.Errorf("Listen = %q, flag 应优先于 env（期望 :21000）", cfg.Server.Listen)
	}
}

func TestLoadInvalidPositiveInt(t *testing.T) {
	clearConfigEnv(t)
	hash, err := HashPSK("secret")
	if err != nil {
		t.Fatalf("HashPSK: %v", err)
	}
	withEnv(t, "LANCHAT_PSK_HASH", hash)
	withEnv(t, "LANCHAT_HEARTBEAT_SEC", "0")
	if _, err := Load("", nil); err == nil {
		t.Fatal("heartbeatSec=0 应报错，实际通过")
	}
}

func TestLoadInvalidPath(t *testing.T) {
	clearConfigEnv(t)
	hash, err := HashPSK("secret")
	if err != nil {
		t.Fatalf("HashPSK: %v", err)
	}
	withEnv(t, "LANCHAT_PSK_HASH", hash)
	withEnv(t, "LANCHAT_PATH", "lctp")
	if _, err := Load("", nil); err == nil {
		t.Fatal("path 不以 / 开头应报错，实际通过")
	}
}

func TestLoadFromYAML(t *testing.T) {
	clearConfigEnv(t)
	hash, err := HashPSK("yaml-secret")
	if err != nil {
		t.Fatalf("HashPSK: %v", err)
	}
	content := "server:\n  listen: ':22000'\n  authMode: psk\n  pskHash: '" + hash + "'\n"
	path := filepath.Join(t.TempDir(), "lanchat.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写临时配置: %v", err)
	}
	cfg, err := Load(path, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Listen != ":22000" {
		t.Errorf("Listen = %q, 期望 :22000", cfg.Server.Listen)
	}
	if cfg.Server.PSKHash != hash {
		t.Errorf("PSKHash 未从 yaml 读取")
	}
}

func TestLoadMissingFileIgnored(t *testing.T) {
	clearConfigEnv(t)
	hash, err := HashPSK("secret")
	if err != nil {
		t.Fatalf("HashPSK: %v", err)
	}
	withEnv(t, "LANCHAT_PSK_HASH", hash)
	if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml"), nil); err != nil {
		t.Fatalf("配置文件不存在时应忽略，得到错误: %v", err)
	}
}

func TestHashPSK(t *testing.T) {
	if _, err := HashPSK(""); err == nil {
		t.Fatal("空口令应报错")
	}
	hash, err := HashPSK("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPSK: %v", err)
	}
	if !strings.HasPrefix(hash, "$2") {
		t.Fatalf("返回值不像 bcrypt 哈希: %q", hash)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("correct horse battery staple")); err != nil {
		t.Fatalf("bcrypt 校验失败: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("wrong")); err == nil {
		t.Fatal("错误口令不应通过 bcrypt 校验")
	}
}
