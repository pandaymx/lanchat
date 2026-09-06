package main

import (
	"testing"

	"github.com/pandaymx/lanchat/internal/i18n"
)

// TestResolveLocale_FlagWinsOverEnv 验证 -lang flag 优先级高于 env 探测。
//
// 用户的 CI / 容器场景下 env 探测到的 $LANG 是 C.UTF-8，但用户
// 想强切 zh-cn 看效果。这里显式给 flag，必须赢。
// 注意 resolveLocale 原样保留用户输入；Bundle.Load 会 ToLower 存，
// 所以调用方传 "zh-CN" / "zh-cn" 都能命中同一个 bundle。
func TestResolveLocale_FlagWinsOverEnv(t *testing.T) {
	got := resolveLocale("zh-CN")
	if got != "zh-CN" {
		t.Fatalf("want zh-CN (flag preserved verbatim), got %q", got)
	}
}

// TestResolveLocale_FlagEmptyUsesEnv 验证 flag 空串 → 走 env 探测。
//
// resolveLocale 内部直接用 os.Environ()，环境变量会污染测试；
// 这里直接走 i18n.DetectLocale 单元测它的 fallback：env 全 C/POSIX
// 时返回 fallback。
func TestResolveLocale_FlagEmptyUsesEnv(t *testing.T) {
	if got := i18n.DetectLocale([]string{"LC_ALL=C", "LANG=POSIX"}, "en"); got != "en" {
		t.Fatalf("DetectLocale(C/POSIX) want en fallback, got %q", got)
	}
	if got := i18n.DetectLocale([]string{}, "en"); got != "en" {
		t.Fatalf("DetectLocale(empty) want en fallback, got %q", got)
	}
}

// TestResolveLocale_UnknownFlagPreserved 验证未加载的 locale 也照原样返回。
//
// resolveLocale 不会做"白名单匹配"，因为 Bundle.T 已经内置 fallback 链。
// 保留字面值便于日志排错（用户能看到自己请求的 ja / fr 而非静默吞掉）。
func TestResolveLocale_UnknownFlagPreserved(t *testing.T) {
	got := resolveLocale("ja")
	if got != "ja" {
		t.Fatalf("want ja (preserved verbatim), got %q", got)
	}
}

// TestResolveLocale_FlagEmpty_EnvZHCN 验证 LC_ALL=zh_CN.UTF-8 → "zh-cn"。
//
// DetectLocale 内部做 BCP 47 简化（replace _ → -、strip .UTF-8、
// 全 lowercase）；具体规则见 i18n.DetectLocale / normalizeTag。
func TestResolveLocale_FlagEmpty_EnvZHCN(t *testing.T) {
	env := []string{"LC_ALL=zh_CN.UTF-8", "LANG=zh_CN.UTF-8"}
	got := i18n.DetectLocale(env, "en")
	if got != "zh-cn" {
		t.Fatalf("want zh-cn (normalized), got %q", got)
	}
}

// TestBundlesEndToEnd_ZHCN 是关键的端到端测试：
// 真实加载 embedded bundles 走 DetectLocale → bundle.ForLocale → Translator.T
// 完整链路，断言最终返回的是 zh-cn 文案。
//
// 之前踩过的坑：bundles 文件名 zh-CN.json 与 DetectLocale 返回的
// zh-cn 大小写不一致，导致走 fallback 拿到英文。这次修 Load/T/Tf
// 内部统一 ToLower，并改文件名为 zh-cn.json，防止再发生。
func TestBundlesEndToEnd_ZHCN(t *testing.T) {
	bundle := i18n.MustLoadEmbedded([]string{"en", "zh-cn"}, "en")
	if got := bundle.ForLocale("zh-cn").T("tui.status.online"); got != "在线" {
		t.Errorf("zh-cn online want %q, got %q", "在线", got)
	}
	if got := bundle.ForLocale("zh-CN").T("tui.status.online"); got != "在线" {
		t.Errorf("zh-CN (mixed case) want %q, got %q", "在线", got)
	}
	if got := bundle.ForLocale("ja").T("tui.status.online"); got != "online" {
		t.Errorf("unknown locale ja want fallback %q, got %q", "online", got)
	}
}
