package event

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"
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

func TestNormalizeWorkflowPathUsesFinalRefDelimiter(t *testing.T) {
	got := NormalizeWorkflowPath(".github/workflows/archive.yaml@candidate.yml@refs/heads/main")
	if got != ".github/workflows/archive.yaml@candidate.yml" {
		t.Fatalf("workflow path = %q", got)
	}
}

func TestStatusDescriptionRejectsFactsTheActionCannotEmit(t *testing.T) {
	for _, description := range []string{
		"dora5:production_exposure",
		"dora5:production_exposure;changed=false",
		"dora5:stage_completed:invented",
	} {
		if _, err := ParseStatusDescription(description); err == nil {
			t.Fatalf("invalid status description was accepted: %q", description)
		}
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

func TestSchemaMatchesRuntimeWorkflowSuffixAndSHAUniqueness(t *testing.T) {
	var schema struct {
		Properties map[string]struct {
			Pattern     string `json:"pattern"`
			MinItems    int    `json:"minItems"`
			UniqueItems bool   `json:"uniqueItems"`
		} `json:"properties"`
		DependentRequired map[string][]string `json:"dependentRequired"`
		AllOf             []struct {
			Then struct {
				Properties map[string]struct {
					MaxItems int `json:"maxItems"`
				} `json:"properties"`
			} `json:"then"`
		} `json:"allOf"`
	}
	if err := json.Unmarshal(Schema, &schema); err != nil {
		t.Fatal(err)
	}
	workflowPattern, err := regexp.Compile(schema.Properties["workflow_path"].Pattern)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{".github/workflows/release.yml", ".github/workflows/release.YAML", ".github/workflows/release.YaMl"} {
		if normalized := NormalizeWorkflowPath(path + "@refs/heads/main"); normalized != path || !workflowPattern.MatchString(normalized) {
			t.Fatalf("runtime and schema disagree for workflow path %q", path)
		}
	}
	changeSHAs := schema.Properties["change_shas"]
	if !changeSHAs.UniqueItems || changeSHAs.MinItems != 1 {
		t.Fatal("schema does not require a non-empty unique change SHA set")
	}
	if !contains(schema.DependentRequired["change_shas"], "change_relation_source") || !contains(schema.DependentRequired["change_relation_source"], "change_shas") || !contains(schema.DependentRequired["work_type"], "work_reason") || !contains(schema.DependentRequired["work_reason"], "work_type") {
		t.Fatal("schema relation and work-purpose dependencies are not bidirectional")
	}
	if len(schema.AllOf) != 1 || schema.AllOf[0].Then.Properties["change_shas"].MaxItems != 1 {
		t.Fatal("schema does not restrict direct_commit to one change SHA")
	}
}

func TestValidateRejectsDuplicateChangeSHAs(t *testing.T) {
	payload := validEventPayload()
	payload.ChangeSHAs = []string{strings.Repeat("a", 40), strings.Repeat("A", 40)}
	if err := Validate(payload); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("duplicate change SHAs error = %v", err)
	}
}

func TestValidateRequiresACompleteDirectCommitRelation(t *testing.T) {
	for _, mutate := range []func(*Payload){
		func(payload *Payload) { payload.ChangeSHAs = nil },
		func(payload *Payload) { payload.ChangeSHAs = []string{strings.Repeat("b", 40)} },
		func(payload *Payload) { payload.ChangeSHAs = append(payload.ChangeSHAs, strings.Repeat("b", 40)) },
	} {
		payload := validEventPayload()
		mutate(&payload)
		if err := Validate(payload); err == nil {
			t.Fatalf("incomplete direct commit relation was accepted: %#v", payload.ChangeSHAs)
		}
	}
}

func TestValidateRejectsOrphanWorkReason(t *testing.T) {
	payload := validEventPayload()
	payload.WorkReason = "scheduled release"
	if err := Validate(payload); err == nil || !strings.Contains(err.Error(), "work_type") {
		t.Fatalf("orphan work reason error = %v", err)
	}
}

func validEventPayload() Payload {
	production, final := true, true
	return Payload{
		EventVersion: Version, EventID: "v1:example/service:1:1:deploy:rollout-1", SourceSystem: "github_actions",
		Repository: "example/service", WorkflowPath: ".github/workflows/deploy.yml", RunID: 1, RunAttempt: 1,
		JobName: "deploy", CommitSHA: strings.Repeat("a", 40), RawEnvironmentName: "production",
		ReleaseUnitRef: "sha256:" + strings.Repeat("b", 64), RolloutGroupKey: "rollout-1", TargetSetID: "production-set",
		DeliveryModel: "github_actions", EventTimeUTC: timeForTest(), ServiceID: "service", TargetID: "production",
		ProductionTraffic: &production, FinalStage: &final, ChangeSHAs: []string{strings.Repeat("a", 40)}, ChangeRelationSource: "direct_commit",
	}
}

func timeForTest() time.Time {
	return time.Unix(1, 0).UTC()
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
