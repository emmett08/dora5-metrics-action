package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emmett08/dora5-metrics-action/event"
	"github.com/emmett08/dora5-metrics-action/internal/githubapi"
)

func validPayload() event.Payload {
	production, final := true, true
	return event.Payload{
		EventVersion: event.Version, EventID: "v1:example/service:1:1:deploy:rollout-1", SourceSystem: "github_actions",
		Repository: "example/service", WorkflowPath: ".github/workflows/deploy.yml", RunID: 1, RunAttempt: 1,
		JobName: "deploy", RawEnvironmentName: "service-production", CommitSHA: strings.Repeat("a", 40),
		ReleaseUnitRef: "sha256:" + strings.Repeat("b", 64), RolloutGroupKey: "rollout-1", DeliveryModel: "github_actions",
		EventTimeUTC: time.Unix(1, 0).UTC(),
		ServiceID:    "service", TargetID: "production", TargetSetID: "production-set", ProductionTraffic: &production, FinalStage: &final,
		ChangeSHAs: []string{strings.Repeat("c", 40)}, ChangeRelationSource: "release_manifest", WorkType: "planned",
	}
}

func TestEventIdentityIsStableAndTargetSpecific(t *testing.T) {
	first := eventIdentity("example/service", 42, "deploy", "rollout-1", "service", "target-a", "set", "production")
	repeated := eventIdentity("example/service", 42, "deploy", "rollout-1", "service", "target-a", "set", "production")
	secondTarget := eventIdentity("example/service", 42, "deploy", "rollout-1", "service", "target-b", "set", "production")
	if first != repeated {
		t.Fatal("same logical rollout stage produced different event IDs")
	}
	if first == secondTarget {
		t.Fatal("different rollout targets produced the same event ID")
	}
	if !strings.HasPrefix(first, "v1:sha256:") || len(first) != len("v1:sha256:")+64 {
		t.Fatalf("unexpected event ID format: %q", first)
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
	for _, name := range []string{"GITHUB_REPOSITORY", "GITHUB_SHA", "GITHUB_RUN_ID", "GITHUB_RUN_ATTEMPT", "GITHUB_JOB", "GITHUB_WORKFLOW_REF"} {
		t.Setenv(name, "")
	}
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

func TestSplitValuesCanonicalizesSHASet(t *testing.T) {
	a := strings.Repeat("a", 40)
	b := strings.Repeat("b", 40)
	got := splitValues(strings.ToUpper(b) + "," + a + "," + b)
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Fatalf("canonical SHA set = %v, want [%s %s]", got, a, b)
	}
}

func TestReconcileStatusRejectsImpossibleTransitions(t *testing.T) {
	started := githubapi.DeploymentStatus{ID: 1, State: "queued", Description: event.StartedDescription, Environment: "production", CreatedAt: time.Unix(1, 0)}
	exposed := githubapi.DeploymentStatus{ID: 2, State: "in_progress", Description: event.ExposureDescription(true), Environment: "production", CreatedAt: time.Unix(2, 0)}
	failed := githubapi.DeploymentStatus{ID: 3, State: "failure", Description: event.CompletionDescription("failure", nil), Environment: "production", CreatedAt: time.Unix(3, 0)}

	for _, test := range []struct {
		name        string
		statuses    []githubapi.DeploymentStatus
		state       string
		description string
	}{
		{name: "replace failure with success", statuses: []githubapi.DeploymentStatus{failed, started}, state: "success", description: event.CompletionDescription("success", nil)},
		{name: "expose after terminal", statuses: []githubapi.DeploymentStatus{failed, started}, state: "in_progress", description: event.ExposureDescription(true)},
		{name: "changed completion without exposure", statuses: []githubapi.DeploymentStatus{started}, state: "success", description: event.CompletionDescription("success", boolPointer(true))},
		{name: "no-op contradicts exposure", statuses: []githubapi.DeploymentStatus{exposed, started}, state: "success", description: event.CompletionDescription("success", boolPointer(false))},
		{name: "restart failed rollout", statuses: []githubapi.DeploymentStatus{failed, started}, state: "queued", description: event.StartedDescription},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := reconcileStatus(test.statuses, "production", true, test.state, test.description); err == nil {
				t.Fatal("invalid lifecycle transition was accepted")
			}
		})
	}
}

func TestReconcileStatusReusesExactFact(t *testing.T) {
	changed := true
	description := event.CompletionDescription("success", &changed)
	statuses := []githubapi.DeploymentStatus{
		{ID: 3, State: "success", Description: description, Environment: "production", CreatedAt: time.Unix(3, 0)},
		{ID: 2, State: "in_progress", Description: event.ExposureDescription(true), Environment: "production", CreatedAt: time.Unix(2, 0)},
		{ID: 1, State: "queued", Description: event.StartedDescription, Environment: "production", CreatedAt: time.Unix(1, 0)},
	}
	id, exists, err := reconcileStatus(statuses, "production", true, "success", description)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || id != 3 {
		t.Fatalf("reconciled status = (%d, %t), want (3, true)", id, exists)
	}
}

func TestReconcileStatusRejectsReverseChronology(t *testing.T) {
	changed := true
	completion := event.CompletionDescription("success", &changed)
	for _, test := range []struct {
		name     string
		statuses []githubapi.DeploymentStatus
	}{
		{
			name: "exposure after terminal",
			statuses: []githubapi.DeploymentStatus{
				{ID: 1, State: "queued", Description: event.StartedDescription, Environment: "production", CreatedAt: time.Unix(1, 0)},
				{ID: 2, State: "success", Description: completion, Environment: "production", CreatedAt: time.Unix(2, 0)},
				{ID: 3, State: "in_progress", Description: event.ExposureDescription(true), Environment: "production", CreatedAt: time.Unix(3, 0)},
			},
		},
		{
			name: "start after exposure",
			statuses: []githubapi.DeploymentStatus{
				{ID: 1, State: "queued", Description: event.StartedDescription, Environment: "production", CreatedAt: time.Unix(1, 0)},
				{ID: 2, State: "in_progress", Description: event.ExposureDescription(true), Environment: "production", CreatedAt: time.Unix(2, 0)},
				{ID: 3, State: "queued", Description: event.StartedDescription, Environment: "production", CreatedAt: time.Unix(3, 0)},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := reconcileStatus(test.statuses, "production", true, "success", completion); err == nil {
				t.Fatal("reverse lifecycle chronology was accepted")
			}
		})
	}
}

func TestReconcileStatusAppliesExposureRulesOnlyToProductionTraffic(t *testing.T) {
	started := githubapi.DeploymentStatus{ID: 1, State: "queued", Description: event.StartedDescription, Environment: "staging", CreatedAt: time.Unix(1, 0)}
	changedCompletion := event.CompletionDescription("success", boolPointer(true))
	if _, exists, err := reconcileStatus([]githubapi.DeploymentStatus{started}, "staging", false, "success", changedCompletion); err != nil || exists {
		t.Fatalf("non-production changed completion = exists:%t error:%v, want a valid new fact", exists, err)
	}
	exposure := event.ExposureDescription(true)
	if _, _, err := reconcileStatus([]githubapi.DeploymentStatus{started}, "staging", false, "in_progress", exposure); err == nil {
		t.Fatal("production exposure was accepted for a non-production target")
	}
}

func TestSelectStartedDeploymentIsOrderIndependent(t *testing.T) {
	startedHigh := startedDeploymentCandidate{deployment: githubapi.Deployment{ID: 30}, statusID: 300, hasStarted: true}
	startedLow := startedDeploymentCandidate{deployment: githubapi.Deployment{ID: 20}, statusID: 200, hasStarted: true}
	statusless := startedDeploymentCandidate{deployment: githubapi.Deployment{ID: 10}}
	for _, candidates := range [][]startedDeploymentCandidate{
		{statusless, startedHigh, startedLow},
		{startedLow, statusless, startedHigh},
		{startedHigh, startedLow, statusless},
	} {
		selected, err := selectStartedDeployment(candidates)
		if err != nil {
			t.Fatal(err)
		}
		if selected.deployment.ID != 20 || selected.statusID != 200 || !selected.hasStarted {
			t.Fatalf("selected candidate = %#v, want lowest deployment with a started fact", selected)
		}
	}
	selected, err := selectStartedDeployment([]startedDeploymentCandidate{{deployment: githubapi.Deployment{ID: 30}}, {deployment: githubapi.Deployment{ID: 10}}})
	if err != nil {
		t.Fatal(err)
	}
	if selected.deployment.ID != 10 {
		t.Fatalf("selected statusless deployment = %d, want 10", selected.deployment.ID)
	}
}

func TestValidateDeploymentIdentityBindsRecordedJobName(t *testing.T) {
	payload := validPayload()
	deployment := githubapi.Deployment{ID: 1, SHA: payload.CommitSHA, Environment: payload.RawEnvironmentName, Payload: payload}
	runtime := runtimeIdentity{
		Repository: payload.Repository, CommitSHA: payload.CommitSHA, WorkflowPath: payload.WorkflowPath,
		RunID: payload.RunID, RunAttempt: payload.RunAttempt, JobName: "job-id",
	}
	if err := validateDeploymentIdentity(deployment, runtime, payload.RawEnvironmentName, "another job"); err == nil {
		t.Fatal("deployment from another recorded job was accepted")
	}
	if err := validateDeploymentIdentity(deployment, runtime, payload.RawEnvironmentName, payload.JobName); err != nil {
		t.Fatal(err)
	}
}

func boolPointer(value bool) *bool {
	return &value
}

type storedDeploymentStatus struct {
	id        int64
	createdAt time.Time
	request   githubapi.CreateDeploymentStatusRequest
}

type fakeDeploymentAPI struct {
	mu                     sync.Mutex
	deployment             *githubapi.CreateDeploymentRequest
	statuses               []storedDeploymentStatus
	deploymentPosts        int
	statusPosts            int
	dropDeploymentResponse bool
	dropStatusResponse     bool
	problems               []string
}

func (fake *fakeDeploymentAPI) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	const deploymentsPath = "/repos/example/service/deployments"
	const deploymentPath = deploymentsPath + "/101"
	const statusesPath = deploymentPath + "/statuses"

	switch {
	case request.Method == http.MethodGet && request.URL.Path == deploymentsPath:
		if request.Body != http.NoBody {
			fake.problems = append(fake.problems, "GET deployment list had a request body")
		}
		if fake.deployment == nil {
			writeJSON(writer, []any{})
			return
		}
		writeJSON(writer, []any{fake.deploymentResponse()})
	case request.Method == http.MethodPost && request.URL.Path == deploymentsPath:
		var input githubapi.CreateDeploymentRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		fake.deploymentPosts++
		fake.deployment = &input
		if fake.dropDeploymentResponse {
			fake.dropDeploymentResponse = false
			dropResponse(writer, fake)
			return
		}
		writeJSON(writer, map[string]any{"id": 101})
	case request.Method == http.MethodGet && request.URL.Path == deploymentPath:
		if fake.deployment == nil {
			http.NotFound(writer, request)
			return
		}
		writeJSON(writer, fake.deploymentResponse())
	case request.Method == http.MethodGet && request.URL.Path == statusesPath:
		statuses := make([]any, 0, len(fake.statuses))
		for index := len(fake.statuses) - 1; index >= 0; index-- {
			status := fake.statuses[index]
			statuses = append(statuses, map[string]any{
				"id": status.id, "state": status.request.State, "description": status.request.Description,
				"environment": status.request.Environment, "environment_url": status.request.EnvironmentURL, "log_url": status.request.LogURL,
				"created_at": status.createdAt.Format(time.RFC3339),
			})
		}
		writeJSON(writer, statuses)
	case request.Method == http.MethodPost && request.URL.Path == statusesPath:
		var input githubapi.CreateDeploymentStatusRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		fake.statusPosts++
		status := storedDeploymentStatus{id: int64(200 + fake.statusPosts), createdAt: time.Unix(int64(fake.statusPosts), 0).UTC(), request: input}
		fake.statuses = append(fake.statuses, status)
		if fake.dropStatusResponse {
			fake.dropStatusResponse = false
			dropResponse(writer, fake)
			return
		}
		writeJSON(writer, map[string]any{"id": status.id})
	default:
		fake.problems = append(fake.problems, request.Method+" "+request.URL.String())
		http.NotFound(writer, request)
	}
}

func (fake *fakeDeploymentAPI) deploymentResponse() map[string]any {
	return map[string]any{
		"id": 101, "sha": fake.deployment.Ref, "task": fake.deployment.Task,
		"environment": fake.deployment.Environment, "payload": fake.deployment.Payload,
	}
}

func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}

func dropResponse(writer http.ResponseWriter, fake *fakeDeploymentAPI) {
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		fake.problems = append(fake.problems, "test server could not hijack response")
		return
	}
	connection, _, err := hijacker.Hijack()
	if err != nil {
		fake.problems = append(fake.problems, "test server could not drop response: "+err.Error())
		return
	}
	_ = connection.Close()
}

func TestEmitLifecycleReconcilesAmbiguousMutations(t *testing.T) {
	fake := &fakeDeploymentAPI{dropDeploymentResponse: true}
	server := httptest.NewServer(fake)
	defer server.Close()
	outputPath := t.TempDir() + "/github-output"
	if err := os.WriteFile(outputPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	setRuntimeEnvironment(t, server.URL, outputPath, 1)

	start := startArguments("production")
	if err := runEmit(start); err == nil {
		t.Fatal("lost deployment response did not fail the first invocation")
	}
	fake.mu.Lock()
	fake.dropStatusResponse = true
	fake.mu.Unlock()
	if err := runEmit(start); err == nil {
		t.Fatal("lost rollout-start response did not fail the second invocation")
	}
	if err := runEmit(start); err != nil {
		t.Fatal(err)
	}

	exposed := []string{"--event=exposed", "--deployment-id=101", "--environment=production", "--release-changed=true"}
	fake.mu.Lock()
	fake.dropStatusResponse = true
	fake.mu.Unlock()
	if err := runEmit(exposed); err == nil {
		t.Fatal("lost exposure response did not fail the first invocation")
	}
	if err := runEmit(exposed); err != nil {
		t.Fatal(err)
	}

	completed := []string{"--event=completed", "--deployment-id=101", "--environment=production", "--release-changed=true", "--result=success"}
	fake.mu.Lock()
	fake.dropStatusResponse = true
	fake.mu.Unlock()
	if err := runEmit(completed); err == nil {
		t.Fatal("lost completion response did not fail the first invocation")
	}
	if err := runEmit(completed); err != nil {
		t.Fatal(err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.deploymentPosts != 1 || fake.statusPosts != 3 || len(fake.statuses) != 3 {
		t.Fatalf("mutations = deployments:%d statuses:%d stored:%d, want 1/3/3", fake.deploymentPosts, fake.statusPosts, len(fake.statuses))
	}
	if len(fake.problems) != 0 {
		t.Fatalf("fake API problems: %v", fake.problems)
	}
	if got := fake.deployment.Payload.ChangeSHAs; len(got) != 1 {
		t.Fatalf("emitted change SHAs = %v, want one deduplicated SHA", got)
	}
	if fake.statuses[0].request.Description != event.StartedDescription || fake.statuses[1].request.Description != event.ExposureDescription(true) || fake.statuses[2].request.Description != event.CompletionDescription("success", boolPointer(true)) {
		t.Fatalf("unexpected lifecycle statuses: %#v", fake.statuses)
	}
	output, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output, []byte("event-id=v1:sha256:")) || !bytes.Contains(output, []byte("deployment-id=101")) || !bytes.Contains(output, []byte("status-id=203")) {
		t.Fatalf("missing reconciliation outputs: %s", output)
	}
}

func TestStartedReconcilesAcrossRunAttempts(t *testing.T) {
	fake := &fakeDeploymentAPI{}
	server := httptest.NewServer(fake)
	defer server.Close()
	outputPath := t.TempDir() + "/github-output"
	if err := os.WriteFile(outputPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	setRuntimeEnvironment(t, server.URL, outputPath, 1)
	if err := runEmit(startArguments("production")); err != nil {
		t.Fatal(err)
	}
	setRuntimeEnvironment(t, server.URL, outputPath, 2)
	if err := runEmit(startArguments("production")); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.deploymentPosts != 1 || fake.statusPosts != 1 {
		t.Fatalf("cross-attempt mutations = deployments:%d statuses:%d, want 1/1", fake.deploymentPosts, fake.statusPosts)
	}
	if fake.deployment.Payload.RunAttempt != 1 {
		t.Fatalf("reconciled deployment attempt = %d, want original attempt 1", fake.deployment.Payload.RunAttempt)
	}
}

func setRuntimeEnvironment(t *testing.T, apiURL, outputPath string, attempt int) {
	t.Helper()
	t.Setenv("GITHUB_REPOSITORY", "example/service")
	t.Setenv("GITHUB_SHA", strings.Repeat("a", 40))
	t.Setenv("GITHUB_RUN_ID", "42")
	t.Setenv("GITHUB_RUN_ATTEMPT", fmt.Sprint(attempt))
	t.Setenv("GITHUB_JOB", "deploy")
	t.Setenv("GITHUB_REF_NAME", "main")
	t.Setenv("GITHUB_EVENT_NAME", "workflow_dispatch")
	t.Setenv("GITHUB_WORKFLOW_REF", "example/service/.github/workflows/deploy.yml@refs/heads/main")
	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("GITHUB_API_URL", apiURL)
	t.Setenv("GITHUB_OUTPUT", outputPath)
}

func startArguments(target string) []string {
	sha := strings.Repeat("a", 40)
	return []string{
		"--event=started", "--environment=production", "--release-unit-ref=sha256:" + strings.Repeat("b", 64),
		"--rollout-group-key=rollout-1", "--service-id=service", "--target-id=" + target, "--target-set-id=production-set",
		"--production-traffic=true", "--final-stage=true", "--change-shas=" + sha + "," + strings.ToUpper(sha),
		"--change-relation-source=direct_commit", "--work-type=planned", "--work-reason=approved release",
	}
}

func runEmit(arguments []string) error {
	return emitCommand(context.Background(), arguments, &bytes.Buffer{}, &bytes.Buffer{})
}
