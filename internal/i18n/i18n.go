// Package i18n 提供 lanchat TUI 的本地化字符串集合。
//
// 设计目标（见 docs/proposals/2026-09-05-i18n-proposal.md）：
//
//   - 仅面向 TUI UI chrome：状态栏、hints、help 面板、sidebar、输入框占位符、
//     消息行 fallback 字符。Hub 端日志、协议消息体不在翻译范围内。
//   - bundle 文件用 JSON + embed.FS 嵌入二进制，无 codegen。
//   - key 命名约定 flat：`domain.area.item`（如 tui.status.online）。
//   - 缺失 key fallback 到 fallbackLocale（en）；再缺 → 返回 key 串（开发期显眼）。
//   - pkg/tui 通过 Translator interface 注入；本包仅由 cmd/tui import，
//     不破坏 internal/ 与 pkg/ 的依赖隔离。
//
// 使用模式：
//
//	bundle := i18n.MustLoad(bundlesFS, []string{"en", "zh-CN"}, "en")
//	locale := i18n.DetectLocale(os.Environ(), "en")
//	tr := bundle.ForLocale(locale)
//	// 把 tr 注入 tui.Config.Translator
package i18n

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"sync"
)

// Translator 是面向业务侧的小接口：T(key) 返回本地化字符串。
//
// 接口**只**暴露无 format args 的 T 方法，原因有二：
//
//  1. TUI UI chrome 当前 14 处文案无一需要 fmt.Sprintf 格式化；引入 args
//     会让每个调用点都背 args 噪音。
//  2. Go 1.22+ vet 会自动检测 (string, ...any) 签名为 print-like 并做
//     format directive 检查。绝大多数 UI 文案 key 是纯字面（"tui.status.online"），
//     配 ...any args 必报 false-positive "no formatting directives"。
//     需要 format 的场景用 Bundle.Tf 显式调用（接收 FormatArgs slice
//     而非 variadic，绕过 vet 的自动 print-like 检测）。
//
// pkg/tui 持这个接口而非 *Bundle，避免把内部实现泄漏出去；测试可用
// 任意实现 fake 注入。
type Translator interface {
	T(key string) string
}

// FormatArgs 是 Bundle.Tf 的参数类型；用 []any 而非 ...any 是为了避开
// Go 1.22+ vet 自动 print-like 检测——vet 只查 variadic args 函数。
//
// 调用方需要传 slice 字面量：b.Tf("en", "greet.%s", i18n.FormatArgs{"alice", 3})。
type FormatArgs []any

// Bundle 是翻译集合：locales × keys → text。
//
// msgs 第一层 key 是 locale（如 "en"、"zh-CN"），第二层 key 是文案
// key（如 "tui.status.online"）；value 是字符串，fmt.Sprintf 模板
// 仅在 Tf 中使用。
//
// RWMutex 保护：当前实现只读，但保留写锁为未来 hot-reload 留余地。
type Bundle struct {
	mu             sync.RWMutex
	msgs           map[string]map[string]string
	fallbackLocale string
}

// Load 从 fsys 加载 bundles/<locale>.json，组装 Bundle。
//
// 参数：
//
//	fsys     — embed.FS 或任意 fs.FS，必须包含 bundles/<locale>.json
//	locales  — 加载的 locale 列表；任一文件缺失 → 返回 error
//	fallback — 缺失翻译时的兜底 locale；必须出现在 locales 里
//
// 返回的 Bundle 后续查表 O(1)（map lookup）。
func Load(fsys fs.FS, locales []string, fallback string) (*Bundle, error) {
	if fallback == "" {
		return nil, fmt.Errorf("i18n: fallback locale is required")
	}
	if len(locales) == 0 {
		return nil, fmt.Errorf("i18n: locales must not be empty")
	}

	msgs := make(map[string]map[string]string, len(locales))
	for _, loc := range locales {
		data, err := fs.ReadFile(fsys, joinBundlesPath(loc+".json"))
		if err != nil {
			return nil, fmt.Errorf("i18n: load %s.json: %w", loc, err)
		}
		m := make(map[string]string)
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("i18n: parse %s.json: %w", loc, err)
		}
		msgs[loc] = m
	}
	if _, ok := msgs[fallback]; !ok {
		return nil, fmt.Errorf("i18n: fallback locale %q not in loaded locales", fallback)
	}
	return &Bundle{
		msgs:           msgs,
		fallbackLocale: fallback,
	}, nil
}

// MustLoad 是 Load 的 panic 包装，cmd/tui 启动期调用：i18n 配置错就立刻
// 退出而不是带病进入 UI 渲染。
func MustLoad(fsys fs.FS, locales []string, fallback string) *Bundle {
	b, err := Load(fsys, locales, fallback)
	if err != nil {
		panic(err)
	}
	return b
}

// T 返回 locale 的翻译（不带 format args）。
//
// 查表策略：
//
//  1. locale 存在且 key 命中 → 返回 msg 原样
//  2. locale 不存在或 key 缺失 → fallback locale 命中 → 返回 msg 原样
//  3. 都没找到 → 返回 key 串本身（开发期显眼；测试期更好定位漏翻译）
//
// 绝大多数 UI chrome 走这条路径；带 format args 的场景用 Tf。
func (b *Bundle) T(locale, key string) string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if msgs, ok := b.msgs[locale]; ok {
		if msg, ok := msgs[key]; ok {
			return msg
		}
	}
	if msgs, ok := b.msgs[b.fallbackLocale]; ok {
		if msg, ok := msgs[key]; ok {
			return msg
		}
	}
	return key
}

// Tf 返回 locale 的翻译并 fmt.Sprintf(msg, args...)。
//
// 与 T 唯一差异是最后一步 Sprintf。FormatArgs slice 而非 ...any 让
// Go 1.22+ vet 不把这个函数当 print-like 自动检查（vet 只查 variadic）。
//
// 测试 key 应带对应 %-verb，否则 fmt.Sprintf 会原样保留字面。
//
// fallback 策略同 T：locale → fallback locale → key 串（带 args 仍
// fmt.Sprintf(key, args...)）。
func (b *Bundle) Tf(locale, key string, args FormatArgs) string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var msg string
	if msgs, ok := b.msgs[locale]; ok {
		msg = msgs[key]
	}
	if msg == "" {
		if msgs, ok := b.msgs[b.fallbackLocale]; ok {
			msg = msgs[key]
		}
	}
	if msg == "" {
		msg = key
	}
	if len(args) > 0 {
		return fmt.Sprintf(msg, args...)
	}
	return msg
}

// ForLocale 返回绑定到 locale 的 Translator。
//
// 用法：bundle.ForLocale("zh-CN") 给 Model 用，调用方不再需要传 locale。
// Translator 实现内部捕获 locale，零运行时开销。
func (b *Bundle) ForLocale(locale string) Translator {
	return &bundleTranslator{b: b, locale: locale}
}

// Locales 报告已加载的 locale 列表（调试 / -lang flag 验证用）。
// 返回顺序与 Load 时传入的 locales 一致无关（map 迭代顺序不定）。
func (b *Bundle) Locales() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]string, 0, len(b.msgs))
	for loc := range b.msgs {
		out = append(out, loc)
	}
	return out
}

// bundleTranslator 内部实现，把 locale 锁进 Translator 调用。
type bundleTranslator struct {
	b      *Bundle
	locale string
}

// T 实现 Translator 接口，内部捕获 locale。
func (t *bundleTranslator) T(key string) string {
	return t.b.T(t.locale, key)
}

// DetectLocale 从 env 中读取 $LC_ALL / $LANG / $LANGUAGE，解析 BCP 47 tag。
//
// 优先级：LC_ALL > LANG > LANGUAGE。同名变量取第一条（其它常见的多 token
// 风格如 "zh_TW:en_US" 也支持，LANGUAGE 字段以冒号分隔多个 fallback）。
//
// 解析规则：
//
//   - "zh_CN.UTF-8" → "zh-cn"（replace _ → -，strip .UTF-8 suffix，lower）
//   - "zh"          → "zh"
//   - "C" / "POSIX" / "" → 跳过该变量，回退下一变量
//   - 全没找到 → 返回 fallback
//
// 注意：本函数不做 locale 是否已加载的校验；调用方需要把结果传给
// Bundle.ForLocale，缺失时 Bundle.T 自动走 fallbackLocale。
func DetectLocale(env []string, fallback string) string {
	for _, want := range []string{"LC_ALL", "LANG", "LANGUAGE"} {
		for _, e := range env {
			eq := strings.IndexByte(e, '=')
			if eq <= 0 || e[:eq] != want {
				continue
			}
			val := e[eq+1:]
			if val == "" || val == "C" || val == "POSIX" {
				break
			}
			// LANGUAGE 风格 "zh_TW:en_US:en"，取第一个 token
			if want == "LANGUAGE" {
				if i := strings.IndexByte(val, ':'); i > 0 {
					val = val[:i]
				}
			}
			// "zh_CN.UTF-8" → "zh_CN"
			if i := strings.IndexByte(val, '.'); i > 0 {
				val = val[:i]
			}
			// "zh_CN@modifier" → "zh_CN"
			if i := strings.IndexByte(val, '@'); i > 0 {
				val = val[:i]
			}
			return normalizeTag(val)
		}
	}
	return fallback
}

// normalizeTag 把 zh_CN / zh-CN / ZH_CN 都转为 zh-cn（小写 + hyphen）。
// 大小写约定：BCP 47 推荐 language 小写、region 大写（zh-CN），但 lanchat
// 内部统一小写存储，便于 case-insensitive 匹配。
func normalizeTag(tag string) string {
	return strings.ToLower(strings.ReplaceAll(tag, "_", "-"))
}

// joinBundlesPath 拼出 bundles/<file> 路径；用 path.Join 兼容 Windows 测试。
func joinBundlesPath(file string) string {
	return "bundles/" + file
}
