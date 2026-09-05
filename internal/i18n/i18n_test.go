package i18n_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/pandaymx/lanchat/internal/i18n"
)

// fakeFS 构造一个最小可用的 bundle fs.FS，方便单测覆盖各种加载场景。
func fakeFS(files map[string]string) fstest.MapFS {
	out := fstest.MapFS{}
	for name, body := range files {
		out["bundles/"+name] = &fstest.MapFile{Data: []byte(body)}
	}
	return out
}

func TestLoad_Success(t *testing.T) {
	fsys := fakeFS(map[string]string{
		"en.json":    `{"foo":"bar","other":"value"}`,
		"zh-CN.json": `{"foo":"酒吧"}`,
	})
	b, err := i18n.Load(fsys, []string{"en", "zh-CN"}, "en")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if got := b.T("en", "foo"); got != "bar" {
		t.Errorf("en foo = %q, want %q", got, "bar")
	}
	if got := b.T("en", "other"); got != "value" {
		t.Errorf("en other = %q, want %q", got, "value")
	}
	if got := b.T("zh-CN", "foo"); got != "酒吧" {
		t.Errorf("zh-CN foo = %q, want %q", got, "酒吧")
	}
}

func TestLoad_MissingBundle(t *testing.T) {
	fsys := fakeFS(map[string]string{"en.json": `{}`})
	_, err := i18n.Load(fsys, []string{"en", "zh-CN"}, "en")
	if err == nil || !strings.Contains(err.Error(), "zh-CN.json") {
		t.Fatalf("expected missing zh-CN.json error, got %v", err)
	}
}

func TestLoad_BadJSON(t *testing.T) {
	fsys := fakeFS(map[string]string{"en.json": `{not json`})
	_, err := i18n.Load(fsys, []string{"en"}, "en")
	if err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestLoad_EmptyArgs(t *testing.T) {
	if _, err := i18n.Load(nil, nil, ""); err == nil {
		t.Fatal("expected error when fallback is empty")
	}
	if _, err := i18n.Load(nil, nil, "en"); err == nil {
		t.Fatal("expected error when locales is empty")
	}
}

func TestLoad_FallbackNotInLocales(t *testing.T) {
	fsys := fakeFS(map[string]string{
		"en.json":    `{}`,
		"zh-CN.json": `{}`,
	})
	_, err := i18n.Load(fsys, []string{"en", "zh-CN"}, "ja")
	if err == nil || !strings.Contains(err.Error(), "fallback locale") {
		t.Fatalf("expected fallback-not-in-locales error, got %v", err)
	}
}

func TestT_FallbackLocaleWhenLocaleMissing(t *testing.T) {
	fsys := fakeFS(map[string]string{
		"en.json":    `{"x":"english"}`,
		"zh-CN.json": `{"x":"中文"}`,
	})
	b, _ := i18n.Load(fsys, []string{"en", "zh-CN"}, "en")
	if got := b.T("ja", "x"); got != "english" {
		t.Errorf("T(ja,x) should fallback to en, got %q", got)
	}
}

func TestT_FallbackLocaleWhenKeyMissing(t *testing.T) {
	fsys := fakeFS(map[string]string{
		"en.json":    `{"only-en":"english-only"}`,
		"zh-CN.json": `{}`,
	})
	b, _ := i18n.Load(fsys, []string{"en", "zh-CN"}, "en")
	if got := b.T("zh-CN", "only-en"); got != "english-only" {
		t.Errorf("missing key in zh-CN should fallback to en, got %q", got)
	}
}

func TestT_MissingKeyReturnsKeyString(t *testing.T) {
	fsys := fakeFS(map[string]string{"en.json": `{}`})
	b, _ := i18n.Load(fsys, []string{"en"}, "en")
	if got := b.T("en", "no.such.key"); got != "no.such.key" {
		t.Errorf("missing key should return key string, got %q", got)
	}
}

func TestTf_FormatArgsApplied(t *testing.T) {
	fsys := fakeFS(map[string]string{
		"en.json": `{"greet.%s":"hello %s, you have %d messages"}`,
	})
	b, _ := i18n.Load(fsys, []string{"en"}, "en")
	got := b.Tf("en", "greet.%s", i18n.FormatArgs{"alice", 3})
	if got != "hello alice, you have 3 messages" {
		t.Errorf("format args not applied: %q", got)
	}
}

func TestTf_FallbackLocaleWithFormat(t *testing.T) {
	fsys := fakeFS(map[string]string{
		"en.json":    `{"greet.%s":"hello %s"}`,
		"zh-CN.json": `{}`,
	})
	b, _ := i18n.Load(fsys, []string{"en", "zh-CN"}, "en")
	got := b.Tf("zh-CN", "greet.%s", i18n.FormatArgs{"alice"})
	if got != "hello alice" {
		t.Errorf("fallback locale Tf should format, got %q", got)
	}
}

func TestTf_NoArgsStillReturns(t *testing.T) {
	fsys := fakeFS(map[string]string{
		"en.json": `{"x":"english"}`,
	})
	b, _ := i18n.Load(fsys, []string{"en"}, "en")
	got := b.Tf("en", "x", nil)
	if got != "english" {
		t.Errorf("Tf with nil args should return msg, got %q", got)
	}
}

func TestForLocale_BindsLocale(t *testing.T) {
	fsys := fakeFS(map[string]string{
		"en.json":    `{"x":"english"}`,
		"zh-CN.json": `{"x":"中文"}`,
	})
	b, _ := i18n.Load(fsys, []string{"en", "zh-CN"}, "en")
	tr := b.ForLocale("zh-CN")
	if got := tr.T("x"); got != "中文" {
		t.Errorf("ForLocale(zh-CN) T(x) = %q, want %q", got, "中文")
	}
}

func TestMustLoad_PanicsOnError(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("MustLoad should panic on error")
		}
	}()
	i18n.MustLoad(fstest.MapFS{}, []string{"en"}, "en")
}

func TestMustLoad_OK(t *testing.T) {
	fsys := fakeFS(map[string]string{"en.json": `{"x":"y"}`})
	b := i18n.MustLoad(fsys, []string{"en"}, "en")
	if got := b.T("en", "x"); got != "y" {
		t.Errorf("MustLoad ok but T returned %q", got)
	}
}

func TestLocales(t *testing.T) {
	fsys := fakeFS(map[string]string{
		"en.json":    `{}`,
		"zh-CN.json": `{}`,
	})
	b, _ := i18n.Load(fsys, []string{"en", "zh-CN"}, "en")
	got := b.Locales()
	if len(got) != 2 {
		t.Errorf("Locales len = %d, want 2", len(got))
	}
}

func TestDetectLocale_PrefersLCAll(t *testing.T) {
	env := []string{
		"LANG=en_US.UTF-8",
		"LC_ALL=zh_CN.UTF-8",
		"LANGUAGE=ja:en",
	}
	if got := i18n.DetectLocale(env, "en"); got != "zh-cn" {
		t.Errorf("LC_ALL should win, got %q", got)
	}
}

func TestDetectLocale_NormalizeVariants(t *testing.T) {
	cases := []struct {
		env  string
		want string
	}{
		{"LANG=zh_CN.UTF-8", "zh-cn"},
		{"LANG=zh-CN", "zh-cn"},
		{"LANG=ZH_CN", "zh-cn"},
		{"LANG=zh", "zh"},
		{"LANG=en_US", "en-us"},
		{"LANG=C", "en"},     // fallback
		{"LANG=POSIX", "en"}, // fallback
		{"LANG=", "en"},      // fallback
	}
	for _, c := range cases {
		t.Run(c.env, func(t *testing.T) {
			got := i18n.DetectLocale([]string{c.env}, "en")
			if got != c.want {
				t.Errorf("DetectLocale(%q) = %q, want %q", c.env, got, c.want)
			}
		})
	}
}

func TestDetectLocale_NoEnvReturnsFallback(t *testing.T) {
	if got := i18n.DetectLocale(nil, "zh-CN"); got != "zh-CN" {
		t.Errorf("empty env should return fallback, got %q", got)
	}
}

func TestDetectLocale_LANGUAGEFirstToken(t *testing.T) {
	env := []string{"LANGUAGE=zh_TW:en_US:en"}
	if got := i18n.DetectLocale(env, "en"); got != "zh-tw" {
		t.Errorf("LANGUAGE first token should win, got %q", got)
	}
}

func TestDetectLocale_StripsModifier(t *testing.T) {
	env := []string{"LANG=zh_CN@euro"}
	if got := i18n.DetectLocale(env, "en"); got != "zh-cn" {
		t.Errorf("@modifier should be stripped, got %q", got)
	}
}
