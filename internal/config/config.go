// Package config 负责信令服务端的配置加载。
// 优先级：CLI flag > 环境变量 LANCHAT_* > yaml 文件 > 默认值。
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

// 鉴权模式枚举。
const (
	AuthModePSK  = "psk"
	AuthModeNone = "none"
)

// 默认监听参数。
const (
	DefaultListen           = ":19090"
	DefaultPath             = "/lctp"
	DefaultHeartbeatSec     = 5
	DefaultIdleTimeoutSec   = 15
	DefaultShutdownGraceSec = 10

	DefaultRelayListen = ":19100"
)

// Config 是服务端配置根。
type Config struct {
	Server ServerConfig `yaml:"server"`
	Relay  RelayConfig  `yaml:"relay"`
}

// ServerConfig 是信令服务器配置。
type ServerConfig struct {
	Listen           string `yaml:"listen"`
	Path             string `yaml:"path"`
	PSKHash          string `yaml:"pskHash"`
	AuthMode         string `yaml:"authMode"`
	HeartbeatSec     int    `yaml:"heartbeatSec"`
	IdleTimeoutSec   int    `yaml:"idleTimeoutSec"`
	ShutdownGraceSec int    `yaml:"shutdownGraceSec"`
}

// RelayConfig 是回退中继数据面配置。
type RelayConfig struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"`
	Host    string `yaml:"host"`  // 宣告给客户端的中继主机（LAN IP）；空=与信令同主机
	Force   bool   `yaml:"force"` // 强制走中继，跳过直连，供测试与排障
}

// Default 返回填好默认值的配置（authMode=psk，PSKHash 需另行提供）。
func Default() Config {
	return Config{
		Server: ServerConfig{
			Listen:           DefaultListen,
			Path:             DefaultPath,
			AuthMode:         AuthModePSK,
			HeartbeatSec:     DefaultHeartbeatSec,
			IdleTimeoutSec:   DefaultIdleTimeoutSec,
			ShutdownGraceSec: DefaultShutdownGraceSec,
		},
		Relay: RelayConfig{
			Enabled: true,
			Listen:  DefaultRelayListen,
		},
	}
}

// field 描述一个可被 flag/env 覆盖的配置项。
type field struct {
	flagKey string
	envKey  string
	set     func(c *Config, v string) error
}

func fields() []field {
	return []field{
		{"listen", "LANCHAT_LISTEN", func(c *Config, v string) error { c.Server.Listen = v; return nil }},
		{"path", "LANCHAT_PATH", func(c *Config, v string) error { c.Server.Path = v; return nil }},
		{"psk-hash", "LANCHAT_PSK_HASH", func(c *Config, v string) error { c.Server.PSKHash = v; return nil }},
		{"auth-mode", "LANCHAT_AUTH_MODE", func(c *Config, v string) error { c.Server.AuthMode = v; return nil }},
		{"heartbeat-sec", "LANCHAT_HEARTBEAT_SEC", func(c *Config, v string) error {
			n, err := positiveInt(v)
			if err != nil {
				return err
			}
			c.Server.HeartbeatSec = n
			return nil
		}},
		{"idle-timeout-sec", "LANCHAT_IDLE_TIMEOUT_SEC", func(c *Config, v string) error {
			n, err := positiveInt(v)
			if err != nil {
				return err
			}
			c.Server.IdleTimeoutSec = n
			return nil
		}},
		{"shutdown-grace-sec", "LANCHAT_SHUTDOWN_GRACE_SEC", func(c *Config, v string) error {
			n, err := positiveInt(v)
			if err != nil {
				return err
			}
			c.Server.ShutdownGraceSec = n
			return nil
		}},
		{"relay-enabled", "LANCHAT_RELAY_ENABLED", func(c *Config, v string) error {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return fmt.Errorf("需要布尔值，得到 %q", v)
			}
			c.Relay.Enabled = b
			return nil
		}},
		{"relay-listen", "LANCHAT_RELAY_LISTEN", func(c *Config, v string) error { c.Relay.Listen = v; return nil }},
		{"relay-host", "LANCHAT_RELAY_HOST", func(c *Config, v string) error { c.Relay.Host = v; return nil }},
		{"relay-force", "LANCHAT_RELAY_FORCE", func(c *Config, v string) error {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return fmt.Errorf("需要布尔值，得到 %q", v)
			}
			c.Relay.Force = b
			return nil
		}},
	}
}

// Load 按 flag > env > yaml > 默认 的优先级加载配置。
// configPath 为空表示不读取文件；flags 为已解析的 CLI flag（仅含用户显式给出的项）。
func Load(configPath string, flags map[string]string) (Config, error) {
	cfg := Default()

	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return cfg, fmt.Errorf("读取配置文件: %w", err)
			}
		} else if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("解析配置文件: %w", err)
		}
	}

	for _, f := range fields() {
		if v, ok := os.LookupEnv(f.envKey); ok {
			if err := f.set(&cfg, v); err != nil {
				return cfg, fmt.Errorf("环境变量 %s: %w", f.envKey, err)
			}
		}
		if v, ok := flags[f.flagKey]; ok {
			if err := f.set(&cfg, v); err != nil {
				return cfg, fmt.Errorf("flag --%s: %w", f.flagKey, err)
			}
		}
	}

	if err := validate(&cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// validate 校验最终配置。
func validate(cfg *Config) error {
	mode := strings.ToLower(cfg.Server.AuthMode)
	if mode != AuthModePSK && mode != AuthModeNone {
		return fmt.Errorf("非法 authMode %q（仅支持 psk/none）", cfg.Server.AuthMode)
	}
	cfg.Server.AuthMode = mode

	if cfg.Server.Listen == "" {
		return errors.New("listen 不能为空")
	}
	if cfg.Server.Path == "" || !strings.HasPrefix(cfg.Server.Path, "/") {
		return fmt.Errorf("path 必须是以 / 开头的非空路径，当前 %q", cfg.Server.Path)
	}
	if cfg.Server.HeartbeatSec <= 0 || cfg.Server.IdleTimeoutSec <= 0 || cfg.Server.ShutdownGraceSec <= 0 {
		return errors.New("heartbeatSec/idleTimeoutSec/shutdownGraceSec 必须为正整数")
	}
	if mode == AuthModePSK && cfg.Server.PSKHash == "" {
		return errors.New("authMode=psk 时必须提供 pskHash（用 `lanchat genpsk` 生成）")
	}

	if cfg.Relay.Enabled && cfg.Relay.Listen == "" {
		return errors.New("relay.enabled=true 时 relay.listen 不能为空")
	}
	return nil
}

// HashPSK 返回明文口令的 bcrypt 哈希，供 `lanchat genpsk` 使用。
func HashPSK(plain string) (string, error) {
	if plain == "" {
		return "", errors.New("口令不能为空")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(plain), 10)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

func positiveInt(v string) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("需要正整数，得到 %q", v)
	}
	return n, nil
}
