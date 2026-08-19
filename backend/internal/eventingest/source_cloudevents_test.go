package eventingest

import (
	"errors"
	"testing"
)

func TestCloudEventsSource_Parse_Canonical(t *testing.T) {
	src := &CloudEventsSource{URI: "cloudevents://primary"}
	body := []byte(`{
		"specversion": "1.0",
		"type": "paladin.object.uploaded",
		"source": "agent-runtime://acme",
		"id": "evt-42",
		"time": "2026-01-01T00:00:00Z",
		"datacontenttype": "application/json",
		"subject": "tenants/0d4f8a3c-3b1e-4a3a-bbbb-cccccccccccc/objectKeys/contracts/objects-by-key/2026/q1.pdf",
		"paladinetag": "abc123",
		"paladinsize": 4096,
		"paladinsequencer": "001",
		"data": {"foo": "bar"}
	}`)
	ev, err := src.Parse(body, "application/json")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ev.Type != EventTypeUploaded {
		t.Errorf("type = %q, want paladin.object.uploaded", ev.Type)
	}
	if ev.Source != "agent-runtime://acme" {
		t.Errorf("source = %q, want envelope value", ev.Source)
	}
	if ev.ID != "evt-42" {
		t.Errorf("id = %q, want envelope id", ev.ID)
	}
	if ev.SubjectFields.TenantID != "0d4f8a3c-3b1e-4a3a-bbbb-cccccccccccc" {
		t.Errorf("tenant = %q", ev.SubjectFields.TenantID)
	}
	if ev.SubjectFields.ObjectKey != "contracts" {
		t.Errorf("object_key = %q", ev.SubjectFields.ObjectKey)
	}
	if ev.SubjectFields.Key != "2026/q1.pdf" {
		t.Errorf("key = %q", ev.SubjectFields.Key)
	}
	if ev.SubjectFields.Etag != "abc123" {
		t.Errorf("etag = %q (lost paladinetag extension)", ev.SubjectFields.Etag)
	}
	if ev.SubjectFields.SizeBytes != 4096 {
		t.Errorf("size = %d (lost paladinsize extension)", ev.SubjectFields.SizeBytes)
	}
}

func TestCloudEventsSource_Parse_BareTripleSubject(t *testing.T) {
	// Some publishers prefer the simpler "<uuid>/<ok>/<key>" form.
	src := &CloudEventsSource{URI: "cloudevents://primary"}
	body := []byte(`{
		"specversion": "1.0",
		"type": "paladin.object.uploaded",
		"source": "test",
		"id": "evt-1",
		"subject": "0d4f8a3c-3b1e-4a3a-bbbb-cccccccccccc/contracts/2026/q1.pdf"
	}`)
	ev, err := src.Parse(body, "")
	if err != nil {
		t.Fatalf("Parse bare: %v", err)
	}
	if ev.SubjectFields.Key != "2026/q1.pdf" {
		t.Errorf("multi-segment key not preserved: %q", ev.SubjectFields.Key)
	}
}

func TestCloudEventsSource_Parse_DeleteEvent(t *testing.T) {
	src := &CloudEventsSource{URI: "cloudevents://primary"}
	body := []byte(`{
		"specversion": "1.0",
		"type": "paladin.object.deleted",
		"source": "test",
		"id": "evt-1",
		"subject": "t/ok/k"
	}`)
	ev, err := src.Parse(body, "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ev.Type != EventTypeDeleted {
		t.Errorf("type = %q, want deleted", ev.Type)
	}
}

func TestCloudEventsSource_Parse_UnknownTypeIgnored(t *testing.T) {
	// Shared-bus tolerance: events with a non-Paladin type return
	// ErrIgnoredEvent so the dedup table doesn't fill with junk.
	src := &CloudEventsSource{URI: "cloudevents://primary"}
	body := []byte(`{
		"specversion": "1.0",
		"type": "com.example.unrelated",
		"source": "test",
		"id": "evt-1",
		"subject": "t/ok/k"
	}`)
	if _, err := src.Parse(body, ""); !errors.Is(err, ErrIgnoredEvent) {
		t.Errorf("non-Paladin type should be ignored, got %v", err)
	}
}

func TestCloudEventsSource_Parse_MissingRequiredAttribute(t *testing.T) {
	src := &CloudEventsSource{URI: "cloudevents://primary"}
	cases := []struct {
		name string
		body string
	}{
		{
			name: "no specversion",
			body: `{"type":"paladin.object.uploaded","source":"s","id":"i","subject":"t/ok/k"}`,
		},
		{
			name: "no type",
			body: `{"specversion":"1.0","source":"s","id":"i","subject":"t/ok/k"}`,
		},
		{
			name: "no source",
			body: `{"specversion":"1.0","type":"paladin.object.uploaded","id":"i","subject":"t/ok/k"}`,
		},
		{
			name: "no id",
			body: `{"specversion":"1.0","type":"paladin.object.uploaded","source":"s","subject":"t/ok/k"}`,
		},
	}
	for _, c := range cases {
		_, err := src.Parse([]byte(c.body), "")
		if !errors.Is(err, ErrUnrecognisedEvent) {
			t.Errorf("%s: err = %v, want ErrUnrecognisedEvent", c.name, err)
		}
	}
}

func TestCloudEventsSource_Parse_EmptySubjectIgnored(t *testing.T) {
	src := &CloudEventsSource{URI: "cloudevents://primary"}
	body := []byte(`{
		"specversion": "1.0",
		"type": "paladin.object.uploaded",
		"source": "test",
		"id": "evt-1",
		"subject": ""
	}`)
	if _, err := src.Parse(body, ""); !errors.Is(err, ErrIgnoredEvent) {
		t.Errorf("empty subject should be ignored, got %v", err)
	}
}

func TestCloudEventsSource_Parse_BadJSON(t *testing.T) {
	src := &CloudEventsSource{URI: "cloudevents://primary"}
	if _, err := src.Parse([]byte("not json"), ""); !errors.Is(err, ErrUnrecognisedEvent) {
		t.Errorf("err = %v, want ErrUnrecognisedEvent", err)
	}
}

func TestParseCloudEventsSubject_Forms(t *testing.T) {
	cases := []struct {
		name       string
		subject    string
		wantOK     bool
		wantTenant string
		wantOK_    string
		wantKey    string
	}{
		{
			name:       "canonical",
			subject:    "tenants/abc/objectKeys/docs/objects-by-key/q1/r.pdf",
			wantOK:     true,
			wantTenant: "abc",
			wantOK_:    "docs",
			wantKey:    "q1/r.pdf",
		},
		{
			name:       "bare triple",
			subject:    "abc/docs/q1/r.pdf",
			wantOK:     true,
			wantTenant: "abc",
			wantOK_:    "docs",
			wantKey:    "q1/r.pdf",
		},
		{
			name:    "two segments only",
			subject: "abc/docs",
			wantOK:  false,
		},
		{
			name:    "empty",
			subject: "",
			wantOK:  false,
		},
		{
			name:    "canonical missing key",
			subject: "tenants/abc/objectKeys/docs/objects-by-key/",
			wantOK:  false,
		},
	}
	for _, c := range cases {
		got, ok := parseCloudEventsSubject(c.subject)
		if ok != c.wantOK {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if got.TenantID != c.wantTenant || got.ObjectKey != c.wantOK_ || got.Key != c.wantKey {
			t.Errorf("%s: parsed = %+v", c.name, got)
		}
	}
}
