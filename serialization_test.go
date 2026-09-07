package epostix

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

type serializationFixture struct {
	Cases []struct {
		Name       string         `json:"name"`
		Type       string         `json:"type"`
		Input      map[string]any `json:"input"`
		Expected   map[string]any `json:"expected"`
		AbsentKeys []string       `json:"absentKeys"`
	} `json:"cases"`
}

func mustTime(t *testing.T, raw string) time.Time {
	t.Helper()

	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatalf("parse time %q: %v", raw, err)
	}

	return parsed
}

func encodeToMap(t *testing.T, value any) map[string]any {
	t.Helper()

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	return out
}

func assertWire(t *testing.T, actual map[string]any, expected map[string]any, absent []string) {
	t.Helper()

	for _, key := range absent {
		if _, present := actual[key]; present {
			t.Errorf("key %q must be absent from the wire body", key)
		}
	}

	for key, want := range expected {
		got, present := actual[key]
		if !present {
			t.Errorf("key %q missing from the wire body", key)
			continue
		}

		wantJSON, _ := json.Marshal(want)
		gotJSON, _ := json.Marshal(got)

		if string(wantJSON) == string(gotJSON) {
			continue
		}

		wantTime, wantErr := time.Parse(time.RFC3339, toString(want))
		gotTime, gotErr := time.Parse(time.RFC3339, toString(got))

		if wantErr == nil && gotErr == nil && wantTime.Equal(gotTime) {
			continue
		}

		t.Errorf("field %s = %s, want %s", key, gotJSON, wantJSON)
	}
}

func toString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}

	return ""
}

func TestTriStateScheduledAt(t *testing.T) {
	omitted := encodeToMap(t, EmailUpdate{})
	if _, present := omitted["scheduled_at"]; present {
		t.Error("an unset Optional must omit the key entirely")
	}

	sendNow := encodeToMap(t, EmailUpdate{ScheduledAt: Null[time.Time]()})
	if value, present := sendNow["scheduled_at"]; !present || value != nil {
		t.Errorf("Null must serialize as an explicit null, got %#v (present=%v)", value, present)
	}

	at := mustTime(t, "2026-09-07T10:00:00Z")
	rescheduled := encodeToMap(t, EmailUpdate{ScheduledAt: Set(at)})

	parsed, err := time.Parse(time.RFC3339, rescheduled["scheduled_at"].(string))
	if err != nil || !parsed.Equal(at) {
		t.Errorf("Set must serialize the instant, got %v", rescheduled["scheduled_at"])
	}
}

func TestFalseAndEmptyStringReachTheWire(t *testing.T) {
	allowDuplicate := false
	text := ""

	body := EmailCreate{
		From:           Addr("a@example.com"),
		To:             []string{"b@example.com"},
		Subject:        "s",
		AllowDuplicate: &allowDuplicate,
		Text:           &text,
	}

	wire := encodeToMap(t, body)

	if value, present := wire["allow_duplicate"]; !present || value != false {
		t.Errorf("allow_duplicate must serialize as false, got %#v (present=%v)", value, present)
	}

	if value, present := wire["text"]; !present || value != "" {
		t.Errorf("an explicit empty string must reach the wire, got %#v (present=%v)", value, present)
	}
}

func TestMailboxEmitsNarrowestForm(t *testing.T) {
	bare := encodeToMap(t, EmailCreate{From: Addr("a@example.com"), To: []string{"b@example.com"}, Subject: "s"})
	if bare["from"] != "a@example.com" {
		t.Errorf("a mailbox without a display name must serialize as a bare string, got %#v", bare["from"])
	}

	named := encodeToMap(t, EmailCreate{From: Named("Billing", "a@example.com"), To: []string{"b@example.com"}, Subject: "s"})

	object, ok := named["from"].(map[string]any)
	if !ok || object["email"] != "a@example.com" || object["name"] != "Billing" {
		t.Errorf("a mailbox with a display name must serialize as an object, got %#v", named["from"])
	}
}

func TestRecipientsAreAlwaysAnArray(t *testing.T) {
	wire := encodeToMap(t, EmailCreate{From: Addr("a@example.com"), To: []string{"b@example.com"}, Subject: "s"})

	recipients, ok := wire["to"].([]any)
	if !ok || len(recipients) != 1 || recipients[0] != "b@example.com" {
		t.Errorf("to must serialize as an array of plain strings, got %#v", wire["to"])
	}
}

func TestNoDefaultIsEverSent(t *testing.T) {
	wire := encodeToMap(t, Attachment{Filename: "r.pdf", Content: "AAEC", ContentType: "application/pdf"})

	if _, present := wire["content_disposition"]; present {
		t.Error("a default the caller did not set must not be sent")
	}
}

func TestAttachmentVariantsSerializeDistinctly(t *testing.T) {
	ref := encodeToMap(t, AttachmentFromUploadedID("att_9"))
	if ref["attachment_id"] != "att_9" || len(ref) != 1 {
		t.Errorf("a reference attachment must carry only its id, got %#v", ref)
	}

	remote := encodeToMap(t, AttachmentFromRemoteURL("r.pdf", "https://example.com/r.pdf"))
	if remote["content_url"] != "https://example.com/r.pdf" || remote["filename"] != "r.pdf" {
		t.Errorf("a remote attachment must carry filename and content_url, got %#v", remote)
	}

	if _, present := remote["content"]; present {
		t.Error("a remote attachment must never carry inline content")
	}
}

func TestSerializationFixtureFileIsPresent(t *testing.T) {
	raw, err := os.ReadFile("testdata/serialization.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var fixture serializationFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	if len(fixture.Cases) == 0 {
		t.Fatal("the shared serialization corpus is empty")
	}
}
