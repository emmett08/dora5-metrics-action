package event

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStatusDescriptionRoundTrip(t *testing.T) {
	changed := true
	description := CompletionDescription("success", &changed)
	fact, err := ParseStatusDescription(description)
	if err != nil {
		t.Fatal(err)
	}
	if fact.Kind != StatusCompleted || fact.Result != "success" || fact.ReleaseChanged == nil || !*fact.ReleaseChanged {
		t.Fatalf("unexpected status fact: %#v", fact)
	}
}

func TestNormalizeWorkflowPath(t *testing.T) {
	got := NormalizeWorkflowPath("/.github/workflows/release.YAML@refs/heads/main")
	if got != ".github/workflows/release.YAML" {
		t.Fatalf("workflow path = %q", got)
	}
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	payload := map[string]any{"event_version": Version, "unexpected": true}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(encoded); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
}

func TestSchemaIsEmbedded(t *testing.T) {
	if !json.Valid(Schema) || !strings.Contains(string(Schema), "DORA 5 rollout") {
		t.Fatal("embedded schema is missing or invalid")
	}
}
