package main

import (
	"strings"
	"testing"

	"github.com/emmett08/dora5-metrics-action/event"
)

func validPayload() event.Payload {
	production, final := true, true
	return event.Payload{
		EventVersion: event.Version, EventID: "v1:example/service:1:1:deploy:rollout-1", SourceSystem: "github_actions",
		Repository: "example/service", WorkflowPath: ".github/workflows/deploy.yml", RunID: 1, RunAttempt: 1,
		JobName: "deploy", RawEnvironmentName: "service-production", CommitSHA: strings.Repeat("a", 40),
		ReleaseUnitRef: "sha256:" + strings.Repeat("b", 64), RolloutGroupKey: "rollout-1", DeliveryModel: "github_actions",
		ServiceID: "service", TargetID: "production", TargetSetID: "production-set", ProductionTraffic: &production, FinalStage: &final,
		ChangeSHAs: []string{strings.Repeat("c", 40)}, ChangeRelationSource: "release_manifest", WorkType: "planned",
	}
}

func TestStartPayloadRequiresImmutableAndAuditableInputs(t *testing.T) {
	payload := validPayload()
	if err := event.Validate(payload); err == nil {
		t.Fatal("work type without a recorded reason was accepted")
	}
	payload.WorkReason = "approved release"
	payload.ChangeRelationSource = "temporal_guess"
	if err := event.Validate(payload); err == nil {
		t.Fatal("unrecognised change relation source was accepted")
	}
	payload.ChangeRelationSource = "release_manifest"
	if err := event.Validate(payload); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeIdentityIsInferredFromGitHubContext(t *testing.T) {
	sha := strings.Repeat("a", 40)
	t.Setenv("GITHUB_REPOSITORY", "example/service")
	t.Setenv("GITHUB_SHA", sha)
	t.Setenv("GITHUB_RUN_ID", "42")
	t.Setenv("GITHUB_RUN_ATTEMPT", "3")
	t.Setenv("GITHUB_JOB", "deploy")
	t.Setenv("GITHUB_REF_NAME", "main")
	t.Setenv("GITHUB_EVENT_NAME", "workflow_dispatch")
	t.Setenv("GITHUB_WORKFLOW_REF", "example/service/.github/workflows/deploy.yml@refs/heads/main")
	identity, err := loadRuntimeIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if identity.Repository != "example/service" || identity.CommitSHA != sha || identity.WorkflowPath != ".github/workflows/deploy.yml" || identity.RunID != 42 || identity.RunAttempt != 3 || identity.JobName != "deploy" || !identity.ManualInvocation {
		t.Fatalf("unexpected inferred identity: %#v", identity)
	}
}

func TestRuntimeIdentityFailsClosedOutsideActions(t *testing.T) {
	if _, err := loadRuntimeIdentity(); err == nil {
		t.Fatal("missing GitHub Actions runtime variables were accepted")
	}
}

func TestDigestReferenceUsesExactLengths(t *testing.T) {
	if !digestReference("registry.example/service@sha256:" + strings.Repeat("a", 64)) {
		t.Fatal("valid digest rejected")
	}
	for _, invalid := range []string{"registry.example/service:latest", "sha256:short", "sha256:" + strings.Repeat("g", 64)} {
		if digestReference(invalid) {
			t.Fatalf("invalid digest accepted: %q", invalid)
		}
	}
}
