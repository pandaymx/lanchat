// Package templates 视图模型单元测试（v1.3：时间分隔线 / 语音）。
package templates

import (
	"strings"
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

// TestNewConvViewsMeta：v3.0 会话列表数据层——metaFn 填充最后消息预览/
// 时间/未读；大厅与群都走数据层；预览截断与时间格式化符合预期。
func TestNewConvViewsMeta(t *testing.T) {
	snaps := []protocol.ConversationSnapshot{
		{Conversation: protocol.Conversation{ID: "g1", Kind: "group", Title: "群一"}, Members: []string{"a", "b"}},
	}
	now := time.Now()
	yesterday := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location()).AddDate(0, 0, -1)
	meta := map[string][3]any{
		"":   {"大厅最后一条", now.UnixMilli(), 3},
		"g1": {"这条消息比较长，" + strings.Repeat("长", 30), yesterday.UnixMilli(), 1},
	}
	fn := func(id string) (string, int64, int) {
		m := meta[id]
		return m[0].(string), m[1].(int64), m[2].(int)
	}
	views := NewConvViews(snaps, "", fn)
	if len(views) != 2 {
		t.Fatalf("views = %d, want 2", len(views))
	}
	if views[0].Unread != 3 || views[0].LastPreview != "大厅最后一条" {
		t.Fatalf("lobby meta wrong: %+v", views[0])
	}
	if views[0].LastAtText == "" {
		t.Fatal("lobby time should be formatted (today)")
	}
	if views[1].Unread != 1 || views[1].LastPreview == "" {
		t.Fatalf("group meta wrong: %+v", views[1])
	}
	if views[1].LastAtText != "昨天" {
		t.Fatalf("group time = %q, want 昨天", views[1].LastAtText)
	}
	if len([]rune(views[1].LastPreview)) > 42 {
		t.Fatalf("preview not clipped: %q", views[1].LastPreview)
	}
}
