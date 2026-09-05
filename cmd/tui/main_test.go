package main

import (
	"os"
	"testing"
)

// TestDefaultDeviceName_NeverEmpty 验证默认设备名永远非空。
//
// 设备标识是 per-device 同步游标的主键（见方案 ADR-008），空值会让
// 多设备同步退化成「所有设备共用一个游标」，所以这里守住非空底线。
func TestDefaultDeviceName_NeverEmpty(t *testing.T) {
	got := defaultDeviceName()
	if got == "" {
		t.Fatal("defaultDeviceName must never return an empty string")
	}
}

// TestDefaultDeviceName_MatchesHostname 验证默认设备名取 hostname，取不到才兜底。
func TestDefaultDeviceName_MatchesHostname(t *testing.T) {
	got := defaultDeviceName()
	host, err := os.Hostname()
	if err != nil || host == "" {
		// 环境拿不到 hostname：应退回 unknownDevice 而不是空串。
		if got != unknownDevice {
			t.Fatalf("when hostname is unavailable, want %q, got %q", unknownDevice, got)
		}
		return
	}
	if got != host {
		t.Fatalf("defaultDeviceName want hostname %q, got %q", host, got)
	}
}
