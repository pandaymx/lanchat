package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/pandaymx/lanchat/pkg/protocol"
)

// runCmd 执行一条 tea.Cmd 并返回其投递的 Msg（batch 里只跑不取）。
func runCmd(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	out := cmd()
	if b, ok := out.(tea.BatchMsg); ok {
		for _, c := range b {
			go c()
		}
		return nil
	}
	return out
}

// ---------- formatSize ----------

func TestFormatSize(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{5 * 1024 * 1024, "5.0 MB"},
		{3 * 1024 * 1024 * 1024, "3.0 GB"},
	}
	for _, c := range cases {
		if got := formatSize(c.in); got != c.want {
			t.Errorf("formatSize(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ---------- formatMessage 附件渲染 ----------

func TestFormatMessage_FileCard(t *testing.T) {
	h := newHistoryView(defaultENTranslator{})
	h.SetSavedPathChecker(func(id string) (string, bool) {
		if id == "m1" {
			return "lanchat-files/a.go", true
		}
		return "", false
	})

	base := protocol.StoredMessage{
		ID:             "m1",
		SenderUserID:   "alice",
		CreatedAt:      time.UnixMilli(1700000000000).UnixMilli(),
		ConversationID: "lobby",
	}
	withFile := base
	withFile.File = &protocol.FileRef{FileID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "a.go", Size: 2048, Mime: "text/x-go"}
	line := formatMessage(&h, withFile)
	if !strings.Contains(line, "[file]") || !strings.Contains(line, "a.go") || !strings.Contains(line, "2.0 KB") {
		t.Errorf("file card missing elements: %q", line)
	}
	if !strings.Contains(line, "saved lanchat-files/a.go") {
		t.Errorf("saved marker missing: %q", line)
	}

	// 未下载的消息：无 saved 标记。
	notSaved := withFile
	notSaved.ID = "m2"
	line2 := formatMessage(&h, notSaved)
	if strings.Contains(line2, "saved") {
		t.Errorf("unsaved file must not show marker: %q", line2)
	}

	// 纯文本消息不受影响。
	plain := base
	plain.Body = "hi"
	line3 := formatMessage(&h, plain)
	if !strings.Contains(line3, "hi") || strings.Contains(line3, "[file]") {
		t.Errorf("plain message changed: %q", line3)
	}
}

// ---------- /file 命令 ----------

type fakeFileSender struct {
	mu   sync.Mutex
	path string
	err  error
}

func (s *fakeFileSender) SendFile(_ context.Context, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.path = path
	return s.err
}

func (s *fakeFileSender) pathFn() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.path
}

type fakeFileReceiver struct {
	mu   sync.Mutex
	msgs []protocol.StoredMessage
	err  error
}

func (r *fakeFileReceiver) SaveFile(_ context.Context, m protocol.StoredMessage) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, m)
	if r.err != nil {
		return "", r.err
	}
	return "lanchat-files/" + m.File.Name, nil
}

func (r *fakeFileReceiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.msgs)
}

// runFileCmd 用 /file 输入走一遍 Update 的 keypress → Enter → trySubmitInput
// 路径（与现有 /clear、/quit 测试同一模式）。
func runFileCmd(t *testing.T, m *Model, text string) tea.Msg {
	t.Helper()
	m.Update(tea.KeyPressMsg{Text: text, Code: '/'})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: 0})
	if cmd == nil {
		t.Fatal("expected a command from slash input")
	}
	return runCmd(cmd)
}

func TestFileCmd_SendsFile(t *testing.T) {
	snd := &fakeFileSender{}
	m := New(Config{User: "alice", Device: "lap", HubURL: "ws://h"})
	m.SetFileTransfer(snd, nil)

	msg := runFileCmd(t, m, "/file /tmp/x.go")
	if _, ok := msg.(sentMsg); !ok {
		t.Fatalf("got %T, want sentMsg", msg)
	}
	if snd.pathFn() != "/tmp/x.go" {
		t.Errorf("sender path = %q", snd.pathFn())
	}
}

func TestFileCmd_NoArgs_ShowsError(t *testing.T) {
	snd := &fakeFileSender{}
	m := New(Config{User: "alice", Device: "lap", HubURL: "ws://h"})
	m.SetFileTransfer(snd, nil)

	msg := runFileCmd(t, m, "/file")
	errMsg, ok := msg.(errMsg)
	if !ok {
		t.Fatalf("got %T, want errMsg", msg)
	}
	if !strings.Contains(errMsg.err.Error(), "usage") {
		t.Errorf("error = %q, want usage hint", errMsg.err)
	}
	if snd.pathFn() != "" {
		t.Errorf("sender must not be called: %q", snd.pathFn())
	}
}

func TestFileCmd_Unsupported(t *testing.T) {
	m := New(Config{User: "alice", Device: "lap", HubURL: "ws://h"})
	msg := runFileCmd(t, m, "/file /tmp/x")
	errMsg, ok := msg.(errMsg)
	if !ok {
		t.Fatalf("got %T, want errMsg", msg)
	}
	if !strings.Contains(errMsg.err.Error(), "not available") {
		t.Errorf("error = %q", errMsg.err)
	}
}

// ---------- 自动下载 ----------

func fileMsg(id, device string) protocol.StoredMessage {
	return protocol.StoredMessage{
		ID:             id,
		SenderUserID:   "bob",
		SenderDeviceID: device,
		File:           &protocol.FileRef{FileID: strings.Repeat("b", 32), Name: "x.bin", Size: 10},
	}
}

func TestDownloadFileCmd_SavesOthersFile(t *testing.T) {
	recv := &fakeFileReceiver{}
	m := New(Config{User: "alice", Device: "lap", HubURL: "ws://h"})
	m.SetFileTransfer(nil, recv)

	msg := fileMsg("m1", "phone")
	cmd := m.downloadFileCmd(msg)
	if cmd == nil {
		t.Fatal("expected download cmd for other device's file")
	}
	got := runCmd(cmd)
	saved, ok := got.(fileSavedMsg)
	if !ok {
		t.Fatalf("got %T, want fileSavedMsg", got)
	}
	if saved.id != "m1" || saved.path != "lanchat-files/x.bin" {
		t.Errorf("saved = %+v", saved)
	}
	if recv.count() != 1 {
		t.Errorf("receiver calls = %d, want 1", recv.count())
	}

	// fileSavedMsg 经 Update → 附件卡片带 saved 标记。
	_, _ = m.Update(saved)
	h := m.history
	h.SetMessages([]protocol.StoredMessage{msg})
	line := formatMessage(&h, msg)
	if !strings.Contains(line, "saved lanchat-files/x.bin") {
		t.Errorf("card after save missing marker: %q", line)
	}
}

func TestDownloadFileCmd_SkipsOwnMessage(t *testing.T) {
	recv := &fakeFileReceiver{}
	m := New(Config{User: "alice", Device: "lap", HubURL: "ws://h"})
	m.SetFileTransfer(nil, recv)

	cmd := m.downloadFileCmd(fileMsg("m1", "lap"))
	if cmd != nil {
		t.Fatal("own device's echo must not trigger download")
	}
	if recv.count() != 0 {
		t.Errorf("receiver calls = %d, want 0", recv.count())
	}
}

func TestDownloadFileCmd_FailureShowsError(t *testing.T) {
	recv := &fakeFileReceiver{err: errors.New("boom")}
	m := New(Config{User: "alice", Device: "lap", HubURL: "ws://h"})
	m.SetFileTransfer(nil, recv)

	cmd := m.downloadFileCmd(fileMsg("m1", "phone"))
	if cmd == nil {
		t.Fatal("expected cmd")
	}
	got := runCmd(cmd)
	errMsg, ok := got.(errMsg)
	if !ok {
		t.Fatalf("got %T, want errMsg", got)
	}
	if !strings.Contains(errMsg.err.Error(), "x.bin") {
		t.Errorf("error = %q, want file name", errMsg.err)
	}
}
