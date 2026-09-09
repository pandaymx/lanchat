// Package templates 视图模型单元测试（v1.3：时间分隔线 / 语音）。
package templates

import (
	"testing"
	"time"

	"github.com/pandaymx/lanchat/pkg/protocol"
)

// fakeTR 是测试用 Translator：命中即返回原 key，保证断言不依赖 i18n 文件。
type fakeTR struct{}

func (fakeTR) T(key string) string { return key }

func TestMarkDayDividers(t *testing.T) {
	now := time.Now()
	ms := func(tm time.Time) int64 { return tm.UnixMilli() }

	cases := []struct {
		name  string
		views []MessageView
		want  []bool // ShowDivider
	}{
		{
			name: "same day no divider after first",
			views: []MessageView{
				{AtMs: ms(now.Add(-2 * time.Hour))},
				{AtMs: ms(now.Add(-1 * time.Hour))},
			},
			want: []bool{true, false},
		},
		{
			name: "cross day inserts divider",
			views: []MessageView{
				{AtMs: ms(now.AddDate(0, 0, -2))},
				{AtMs: ms(now.AddDate(0, 0, -1))},
				{AtMs: ms(now)},
			},
			want: []bool{true, true, true},
		},
		{
			name: "empty atMs guarded",
			views: []MessageView{
				{AtMs: 0},
				{AtMs: ms(now)},
			},
			// AtMs=0 的首条 dayKey 为空、与 prevDay 相同 → 不标；
			// 第二条日期变化 → 标（含"未知时间的消息"之后出现分隔）。
			want: []bool{false, true},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			MarkDayDividers(fakeTR{}, c.views)
			for i, w := range c.want {
				if c.views[i].ShowDivider != w {
					t.Fatalf("view[%d] ShowDivider = %v, want %v", i, c.views[i].ShowDivider, w)
				}
			}
			if c.views[0].ShowDivider && c.views[0].DividerText == "" {
				t.Fatal("first divider must have non-empty text")
			}
		})
	}
}

func TestDayDividerText(t *testing.T) {
	now := time.Now()
	if got := dayDividerText(fakeTR{}, now.UnixMilli()); got != "web.day.today" {
		t.Fatalf("today = %q, want web.day.today", got)
	}
	if got := dayDividerText(fakeTR{}, now.AddDate(0, 0, -1).UnixMilli()); got != "web.day.yesterday" {
		t.Fatalf("yesterday = %q, want web.day.yesterday", got)
	}
	old := dayDividerText(fakeTR{}, now.AddDate(-1, 0, -1).UnixMilli())
	if old == "" || old == "web.day.today" || old == "web.day.yesterday" {
		t.Fatalf("old date = %q, want ISO date", old)
	}
}

func TestMessageViewIsAudio(t *testing.T) {
	audio := &protocol.FileRef{FileID: "f1", Mime: "audio/webm"}
	img := &protocol.FileRef{FileID: "f2", Mime: "image/png"}
	if !(MessageView{File: audio}).IsAudio() {
		t.Fatal("audio/webm should be audio")
	}
	if (MessageView{File: img}).IsAudio() {
		t.Fatal("image/png should not be audio")
	}
	if (MessageView{}).IsAudio() {
		t.Fatal("nil file should not be audio")
	}
}

func TestVoiceWaveHeights(t *testing.T) {
	hs := VoiceWaveHeights("abc")
	if len(hs) != 24 {
		t.Fatalf("heights len = %d, want 24", len(hs))
	}
	for i, h := range hs {
		if h < 30 || h > 100 {
			t.Fatalf("height[%d] = %d, want 30-100", i, h)
		}
	}
	again := VoiceWaveHeights("abc")
	for i := range hs {
		if hs[i] != again[i] {
			t.Fatalf("not deterministic at %d", i)
		}
	}
}
