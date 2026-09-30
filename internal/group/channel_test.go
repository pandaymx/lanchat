package group

import (
	"errors"
	"testing"
)

type chanSink struct {
	count int
}

func (c *chanSink) OnMatrix(MatrixEvent)   {}
func (c *chanSink) OnChannel(ChannelEvent) { c.count++ }

func TestChannelCreateJoinLeave(t *testing.T) {
	reg := NewRegistry(&chanSink{})
	id, err := reg.Create("设计组", "alice", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Join(id, "bob"); err != nil {
		t.Fatal(err)
	}
	if !reg.IsMember(id, "bob") {
		t.Fatal("bob 加入后不是成员")
	}
	rec, err := reg.Recipients(id, "alice")
	if err != nil || len(rec) != 1 || rec[0] != "bob" {
		t.Fatalf("扇出对象 = %v err=%v", rec, err)
	}

	if err := reg.Leave(id, "bob"); err != nil {
		t.Fatal(err)
	}
	if reg.IsMember(id, "bob") {
		t.Fatal("bob 退出后仍是成员")
	}
}

func TestChannelMessageMembersOnly(t *testing.T) {
	reg := NewRegistry(&chanSink{})
	id, _ := reg.Create("私密", "alice", false)

	// 非成员发送应被拒绝。
	if _, err := reg.Recipients(id, "eve"); !errors.Is(err, errNotMember) {
		t.Fatalf("非成员扇出 err=%v, want errNotMember", err)
	}
	// 不存在的频道。
	if _, err := reg.Recipients("nope", "alice"); !errors.Is(err, errChannelNotFound) {
		t.Fatalf("未知频道 err=%v, want errChannelNotFound", err)
	}
}

func TestPrivateChannelRequiresInvite(t *testing.T) {
	reg := NewRegistry(&chanSink{})
	id, _ := reg.Create("私有", "alice", true)

	// 非受邀成员直接加入 private 频道被拒。
	if err := reg.Join(id, "bob"); !errors.Is(err, errNotMember) {
		t.Fatalf("未邀请加入 private err=%v, want errNotMember", err)
	}
	// 非 owner 不能邀请。
	if err := reg.Invite(id, "bob", "carol"); !errors.Is(err, errNotOwner) {
		t.Fatalf("非 owner 邀请 err=%v, want errNotOwner", err)
	}
	// owner 邀请后可加入（成员关系已在 Invite 时建立）。
	if err := reg.Invite(id, "alice", "bob"); err != nil {
		t.Fatalf("owner 邀请 err=%v", err)
	}
	if !reg.IsMember(id, "bob") {
		t.Fatal("受邀后 bob 不是成员")
	}
}

func TestOwnerLeaveTransfersOwnership(t *testing.T) {
	reg := NewRegistry(&chanSink{})
	id, _ := reg.Create("组", "alice", false)
	_ = reg.Join(id, "bob")
	_ = reg.Join(id, "carol")

	if err := reg.Leave(id, "alice"); err != nil {
		t.Fatal(err)
	}
	chans := reg.List()
	if len(chans) != 1 {
		t.Fatalf("频道数 = %d, want 1", len(chans))
	}
	if chans[0].OwnerID != "bob" {
		t.Fatalf("owner 移交 = %q, want bob", chans[0].OwnerID)
	}

	// 全员退出后频道销毁。
	_ = reg.Leave(id, "bob")
	_ = reg.Leave(id, "carol")
	if len(reg.List()) != 0 {
		t.Fatal("全员退出后频道应销毁")
	}
}

func TestRemoveOfflineMember(t *testing.T) {
	reg := NewRegistry(&chanSink{})
	id, _ := reg.Create("组", "alice", false)
	_ = reg.Join(id, "bob")

	affected := reg.Remove("bob")
	if len(affected) != 1 || affected[0] != id {
		t.Fatalf("离线清理 affected=%v", affected)
	}
	if reg.IsMember(id, "bob") {
		t.Fatal("清理后 bob 仍是成员")
	}
}
