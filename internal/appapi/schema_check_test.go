package appapi_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/pandaymx/lanchat/internal/appapi"
)

// schemaFile 契约文件相对仓库根的位置（本测试位于 internal/appapi）。
const schemaFile = "../../api/ipc.schema.json"

// wantNotifications schema 中必须存在的全部事件（与 Listener 回调一一对应）。
var wantNotifications = []string{
	"conn.changed",
	"peer.joined",
	"peer.left",
	"msg.received",
	"transfer.progress",
	"transfer.done",
	"transfer.failed",
	"group.matrix",
	"channel.updated",
}

type schemaDoc struct {
	SchemaVersion string `json:"schemaVersion"`
	Requests      map[string]json.RawMessage
	Notifications map[string]json.RawMessage
	Enums         map[string][]string
	Types         map[string]map[string]string
}

func loadSchema(t *testing.T) *schemaDoc {
	t.Helper()

	path, err := filepath.Abs(schemaFile)
	if err != nil {
		t.Fatalf("resolve schema path: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	var doc schemaDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	return &doc
}

func TestSchemaIsValidV1(t *testing.T) {
	doc := loadSchema(t)
	if doc.SchemaVersion != "1.0" {
		t.Errorf("schemaVersion = %q, want 1.0", doc.SchemaVersion)
	}
	if len(doc.Requests) == 0 || len(doc.Notifications) == 0 {
		t.Fatal("schema must define requests and notifications")
	}
}

func TestAPIMethodsMatchSchema(t *testing.T) {
	doc := loadSchema(t)

	// 反射 appapi.API 的全部方法名。
	var iface appapi.API
	rt := reflect.TypeOf(&iface).Elem()

	ifaceMethods := make([]string, 0, rt.NumMethod())
	for i := 0; i < rt.NumMethod(); i++ {
		ifaceMethods = append(ifaceMethods, rt.Method(i).Name)
	}
	sort.Strings(ifaceMethods)

	schemaMethods := make([]string, 0, len(doc.Requests))
	for name := range doc.Requests {
		schemaMethods = append(schemaMethods, name)
	}
	sort.Strings(schemaMethods)

	if !reflect.DeepEqual(ifaceMethods, schemaMethods) {
		t.Fatalf("appapi.API 与 schema requests 不一致\n interface=%v\n schema   =%v",
			ifaceMethods, schemaMethods)
	}
}

func TestSchemaNotifications(t *testing.T) {
	doc := loadSchema(t)

	got := make([]string, 0, len(doc.Notifications))
	for name := range doc.Notifications {
		got = append(got, name)
	}
	sort.Strings(got)

	want := append([]string(nil), wantNotifications...)
	sort.Strings(want)

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("notifications mismatch\n got =%v\n want=%v", got, want)
	}
}

func TestSchemaEnumsMatchConstants(t *testing.T) {
	doc := loadSchema(t)

	want := map[string][]string{
		"ConnState": {
			string(appapi.ConnDisconnected),
			string(appapi.ConnConnecting),
			string(appapi.ConnConnected),
			string(appapi.ConnAuthFailed),
		},
		"TransferState": {
			string(appapi.TransferPending),
			string(appapi.TransferActive),
			string(appapi.TransferPaused),
			string(appapi.TransferDone),
			string(appapi.TransferFailed),
			string(appapi.TransferCanceled),
		},
		"TransferDirection": {
			string(appapi.TransferInbound),
			string(appapi.TransferOutbound),
		},
		"PathKind": {
			string(appapi.PathUnicast),
			string(appapi.PathSwarm),
			string(appapi.PathChannel),
		},
	}

	for name, vals := range want {
		sort.Strings(vals)
		got := append([]string(nil), doc.Enums[name]...)
		sort.Strings(got)
		if !reflect.DeepEqual(got, vals) {
			t.Errorf("enum %s mismatch\n got =%v\n want=%v", name, got, vals)
		}
	}
}

func TestSchemaTypeFields(t *testing.T) {
	doc := loadSchema(t)

	// 每个在 requests/notifications 中引用的复合类型都必须在 types 中定义。
	for _, name := range []string{"Peer", "Server", "Transfer", "Channel"} {
		if len(doc.Types[name]) == 0 {
			t.Errorf("type %s is not defined in schema", name)
		}
	}
}
