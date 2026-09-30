package protocol

import (
	"encoding/json"
	"testing"
)

func TestNewEnvelopeAndPayloadRoundTrip(t *testing.T) {
	payload := &TextPayload{Text: "你好"}
	env, err := NewEnvelope("msg-1", TextMsg, payload)
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	if env.V != envelopeV {
		t.Errorf("V = %d, want %d", env.V, envelopeV)
	}
	if env.Type != TextMsg {
		t.Errorf("Type = %q, want %q", env.Type, TextMsg)
	}
	if env.TS == 0 {
		t.Error("TS not set")
	}

	var got TextPayload
	if err := env.DecodePayload(&got); err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	if got.Text != "你好" {
		t.Errorf("Text = %q, want %q", got.Text, "你好")
	}
}

func TestEnvelopeJSONRoundTrip(t *testing.T) {
	env := &Envelope{
		V:     envelopeV,
		Type:  Hello,
		ID:    "id-1",
		From:  "u1",
		To:    "u2",
		Group: GroupBroadcast,
		Seq:   7,
		TS:    123456,
	}
	data, err := env.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := UnmarshalEnvelope(data)
	if err != nil {
		t.Fatalf("UnmarshalEnvelope: %v", err)
	}
	if got.Type != env.Type || got.ID != env.ID || got.From != env.From ||
		got.To != env.To || got.Group != env.Group || got.Seq != env.Seq || got.TS != env.TS {
		t.Errorf("round trip mismatch: %+v", got)
	}
}

func TestEnvelopeOmitEmpty(t *testing.T) {
	env := &Envelope{V: envelopeV, Type: Heartbeat, ID: "id-2", TS: 1}
	data, err := env.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(data)
	for _, field := range []string{`"from"`, `"to"`, `"group"`, `"seq"`, `"replyTo"`, `"payload"`} {
		if contains(s, field) {
			t.Errorf("field %s should be omitted, json=%s", field, s)
		}
	}
}

func TestGroupSemantics(t *testing.T) {
	if GroupUnicast != "" {
		t.Error("GroupUnicast must be empty string")
	}
	if GroupBroadcast != "*" {
		t.Errorf("GroupBroadcast = %q, want *", GroupBroadcast)
	}
}

func TestDecodePayloadEmpty(t *testing.T) {
	env := &Envelope{}
	if err := env.DecodePayload(&TextPayload{}); err != nil {
		t.Errorf("empty payload decode should be nil, got %v", err)
	}
}

// TestGroupOfferPayloadRoundTrip 验证数据面 Candidates/Token 可往返序列化。
func TestGroupOfferPayloadRoundTrip(t *testing.T) {
	offer := GroupOfferPayload{
		GroupID:    "g1",
		TransferID: "t1",
		Name:       "f.bin",
		Size:       123,
		BlockCount: 2,
		SHA256:     "abc",
		Candidates: []string{"10.0.0.1:9000", "192.168.1.2:9000"},
		Token:      "tok-1",
	}
	data, err := json.Marshal(offer)
	if err != nil {
		t.Fatalf("marshal offer: %v", err)
	}
	var got GroupOfferPayload
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal offer: %v", err)
	}
	if got.Token != offer.Token || len(got.Candidates) != len(offer.Candidates) {
		t.Errorf("offer round trip mismatch: %+v", got)
	}

	join := GroupJoinPayload{
		GroupID: "g1", TransferID: "t1",
		Candidates: []string{"10.0.0.2:9001"}, Token: "tok-2",
	}
	jdata, err := json.Marshal(join)
	if err != nil {
		t.Fatalf("marshal join: %v", err)
	}
	var gotJoin GroupJoinPayload
	if err := json.Unmarshal(jdata, &gotJoin); err != nil {
		t.Fatalf("unmarshal join: %v", err)
	}
	if gotJoin.Token != join.Token || len(gotJoin.Candidates) != 1 {
		t.Errorf("join round trip mismatch: %+v", gotJoin)
	}
}

// TestGroupPayloadsBackwardCompatible 验证旧端发来的载荷（无新字段）可缺省解析。
func TestGroupPayloadsBackwardCompatible(t *testing.T) {
	for _, raw := range []string{
		`{"groupID":"g1","transferID":"t1","name":"f","size":1,"blockCount":1,"sha256":"x"}`,
		`{"groupID":"g1","transferID":"t1"}`,
	} {
		var offer GroupOfferPayload
		if err := json.Unmarshal([]byte(raw), &offer); err != nil {
			t.Errorf("offer default parse failed for %s: %v", raw, err)
		}
		if offer.Candidates != nil || offer.Token != "" {
			t.Errorf("new fields must default to zero: %+v", offer)
		}
	}

	var join GroupJoinPayload
	if err := json.Unmarshal([]byte(`{"groupID":"g1","transferID":"t1"}`), &join); err != nil {
		t.Fatalf("join default parse: %v", err)
	}
	if join.Candidates != nil || join.Token != "" {
		t.Errorf("join new fields must default to zero: %+v", join)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
