package main

import (
	"testing"
)

func TestToCamelCase(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"GetState", "getState"},
		{"Connect", "connect"},
		{"BrowseServers", "browseServers"},
		{"ChannelCreate", "channelCreate"},
		{"OfferFileToGroup", "offerFileToGroup"},
		{"", ""},
		{"a", "a"},
		{"A", "a"},
	}
	for _, tc := range tests {
		got := toCamelCase(tc.in)
		if got != tc.want {
			t.Errorf("toCamelCase(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMatchMethodJSONRPC(t *testing.T) {
	content := `
func foo() {
    ipc.InvokeAsync("GetState", null)
    ipc.InvokeAsync("Connect", new { addr = "x" })
    // "SendText" is also referenced here
}
`
	if !matchMethod(content, "GetState", "jsonrpc") {
		t.Error("should match GetState")
	}
	if !matchMethod(content, "Connect", "jsonrpc") {
		t.Error("should match Connect")
	}
	if !matchMethod(content, "SendText", "jsonrpc") {
		t.Error("should match SendText")
	}
	if matchMethod(content, "CancelFile", "jsonrpc") {
		t.Error("should NOT match CancelFile")
	}
	// JSON-RPC 带引号匹配：子串不应误匹配。
	if matchMethod(content, "Get", "jsonrpc") {
		t.Error("should NOT match partial 'Get'")
	}
	// "OfferFile" 不应匹配 "OfferFileToGroup"。
	content2 := `ipc.InvokeAsync("OfferFileToGroup", params)`
	if matchMethod(content2, "OfferFile", "jsonrpc") {
		t.Error("should NOT match OfferFile when only OfferFileToGroup present")
	}
	if !matchMethod(content2, "OfferFileToGroup", "jsonrpc") {
		t.Error("should match OfferFileToGroup")
	}
}

func TestMatchMethodGomobile(t *testing.T) {
	content := `
client.getStateJSON()
client.connect(addr, psk: psk)
client.channelCreate(name, topic: topic, private: false)
client.offerFileToGroup(group, path: path)
client.sendText(to, text: text, group: "")
`
	if !matchMethod(content, "GetState", "gomobile") {
		t.Error("should match getState (from getStateJSON)")
	}
	if !matchMethod(content, "Connect", "gomobile") {
		t.Error("should match connect")
	}
	if !matchMethod(content, "ChannelCreate", "gomobile") {
		t.Error("should match channelCreate")
	}
	if !matchMethod(content, "OfferFileToGroup", "gomobile") {
		t.Error("should match offerFileToGroup")
	}
	if !matchMethod(content, "SendText", "gomobile") {
		t.Error("should match sendText")
	}
	if matchMethod(content, "CancelFile", "gomobile") {
		t.Error("should NOT match cancelFile (not in content)")
	}
}

func TestMatchMethodGomobileAndroid(t *testing.T) {
	content := `
client!!.stateJSON
client!!.connect(addr, psk)
client!!.channelCreate(name, topic, isPrivate)
client!!.pickDownloadDir(path)
`
	if !matchMethod(content, "GetState", "gomobile") {
		t.Error("should match stateJSON")
	}
	if !matchMethod(content, "Connect", "gomobile") {
		t.Error("should match connect")
	}
	if !matchMethod(content, "ChannelCreate", "gomobile") {
		t.Error("should match channelCreate")
	}
	if !matchMethod(content, "PickDownloadDir", "gomobile") {
		t.Error("should match pickDownloadDir")
	}
}

func TestToSet(t *testing.T) {
	s := toSet([]string{"a", "b", "c"})
	if len(s) != 3 || !s["a"] || !s["b"] || !s["c"] {
		t.Errorf("toSet = %v", s)
	}
	if s["d"] {
		t.Error("should not contain d")
	}
}

func TestSortedKeys(t *testing.T) {
	m := map[string]platform{
		"z": {Name: "z"},
		"a": {Name: "a"},
		"m": {Name: "m"},
	}
	keys := sortedKeys(m)
	if len(keys) != 3 || keys[0] != "a" || keys[1] != "m" || keys[2] != "z" {
		t.Errorf("sortedKeys = %v", keys)
	}
}
