package protocol

import (
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

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
