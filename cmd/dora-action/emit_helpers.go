package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emmett08/dora5-metrics-action/event"
	"github.com/emmett08/dora5-metrics-action/internal/githubapi"
)

type runtimeIdentity struct {
	Repository       string
	CommitSHA        string
	WorkflowPath     string
	RunID            int64
	RunAttempt       int
	JobName          string
	RefName          string
	ManualInvocation bool
}

func tokenFromEnvironment(name string) (string, error) {
	token := strings.TrimSpace(os.Getenv(name))
	if token == "" {
		return "", fmt.Errorf("%s is not set", name)
	}
	return token, nil
}

func loadRuntimeIdentity() (runtimeIdentity, error) {
	identity := runtimeIdentity{Repository: os.Getenv("GITHUB_REPOSITORY"), CommitSHA: os.Getenv("GITHUB_SHA"), JobName: os.Getenv("GITHUB_JOB"), RefName: os.Getenv("GITHUB_REF_NAME"), ManualInvocation: os.Getenv("GITHUB_EVENT_NAME") == "workflow_dispatch"}
	workflowRef := os.Getenv("GITHUB_WORKFLOW_REF")
	missing := make([]string, 0)
	for name, value := range map[string]string{"GITHUB_REPOSITORY": identity.Repository, "GITHUB_SHA": identity.CommitSHA, "GITHUB_JOB": identity.JobName, "GITHUB_WORKFLOW_REF": workflowRef} {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return runtimeIdentity{}, fmt.Errorf("missing GitHub Actions runtime variables: %s", strings.Join(missing, ", "))
	}
	if !hexIdentifier(identity.CommitSHA, 40, 64) {
		return runtimeIdentity{}, errors.New("GITHUB_SHA must be a 40- or 64-character Git commit ID")
	}
	var err error
	identity.RunID, err = positiveRuntimeInteger("GITHUB_RUN_ID")
	if err != nil {
		return runtimeIdentity{}, err
	}
	attempt, err := positiveRuntimeInteger("GITHUB_RUN_ATTEMPT")
	if err != nil {
		return runtimeIdentity{}, err
	}
	if attempt > int64(^uint(0)>>1) {
		return runtimeIdentity{}, errors.New("GITHUB_RUN_ATTEMPT is too large")
	}
	identity.RunAttempt = int(attempt)
	prefix := identity.Repository + "/"
	if !strings.HasPrefix(strings.ToLower(workflowRef), strings.ToLower(prefix)) {
		return runtimeIdentity{}, errors.New("GITHUB_WORKFLOW_REF does not identify the runtime repository")
	}
	identity.WorkflowPath = event.NormalizeWorkflowPath(workflowRef[len(prefix):])
	if !strings.HasPrefix(identity.WorkflowPath, ".github/workflows/") {
		return runtimeIdentity{}, errors.New("GITHUB_WORKFLOW_REF does not contain a workflow path")
	}
	return identity, nil
}

func positiveRuntimeInteger(name string) (int64, error) {
	value, err := strconv.ParseInt(os.Getenv(name), 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}

func digestReference(value string) bool {
	lower := strings.ToLower(value)
	if index := strings.LastIndex(lower, "@sha256:"); index >= 0 {
		return hexIdentifier(lower[index+len("@sha256:"):], 64)
	}
	if index := strings.LastIndex(lower, "@sha512:"); index >= 0 {
		return hexIdentifier(lower[index+len("@sha512:"):], 128)
	}
	if strings.HasPrefix(lower, "sha256:") {
		return hexIdentifier(lower[len("sha256:"):], 64)
	}
	if strings.HasPrefix(lower, "sha512:") {
		return hexIdentifier(lower[len("sha512:"):], 128)
	}
	return false
}

func hexIdentifier(value string, lengths ...int) bool {
	validLength := false
	for _, length := range lengths {
		if len(value) == length {
			validLength = true
			break
		}
	}
	if !validLength {
		return false
	}
	for _, character := range strings.ToLower(value) {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func requiredBoolean(name, value string) (bool, error) {
	if value == "" {
		return false, fmt.Errorf("%s is required", name)
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", name)
	}
	return parsed, nil
}

func optionalBoolean(name, value string) (*bool, error) {
	if value == "" || value == "unknown" {
		return nil, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return nil, fmt.Errorf("%s must be true, false, or unknown", name)
	}
	return &parsed, nil
}

func validateTextInput(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("%s contains a control character", name)
	}
	return nil
}

func validateOptionalHTTPURL(name, value string) error {
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%s must be an absolute HTTP or HTTPS URL without credentials", name)
	}
	return nil
}

func sameEventIntent(existing, requested event.Payload) bool {
	existing.EventID = requested.EventID
	existing.RunAttempt = requested.RunAttempt
	existing.EventTimeUTC = requested.EventTimeUTC
	return reflect.DeepEqual(existing, requested)
}

func sameLogicalStage(existing, requested event.Payload) bool {
	return existing.Repository == requested.Repository &&
		existing.WorkflowPath == requested.WorkflowPath &&
		existing.RunID == requested.RunID &&
		existing.JobName == requested.JobName &&
		existing.CommitSHA == requested.CommitSHA &&
		existing.RawEnvironmentName == requested.RawEnvironmentName &&
		existing.RolloutGroupKey == requested.RolloutGroupKey &&
		existing.ServiceID == requested.ServiceID &&
		existing.TargetID == requested.TargetID &&
		existing.TargetSetID == requested.TargetSetID
}

func validateDeploymentIdentity(deployment githubapi.Deployment, runtime runtimeIdentity, environment, jobName string) error {
	if deployment.SHA != runtime.CommitSHA || deployment.Payload.CommitSHA != runtime.CommitSHA {
		return errors.New("deployment commit does not match the current workflow commit")
	}
	if deployment.Environment != environment || deployment.Payload.RawEnvironmentName != environment {
		return errors.New("deployment environment does not match the requested environment")
	}
	if deployment.Payload.Repository != runtime.Repository || deployment.Payload.WorkflowPath != runtime.WorkflowPath || deployment.Payload.RunID != runtime.RunID || deployment.Payload.RunAttempt > runtime.RunAttempt {
		return errors.New("deployment does not belong to the current workflow run attempt")
	}
	if deployment.Payload.JobName != jobName {
		return errors.New("deployment job does not match the current recorded job name")
	}
	return nil
}

type startedDeploymentCandidate struct {
	deployment githubapi.Deployment
	statusID   int64
	hasStarted bool
}

func selectStartedDeployment(candidates []startedDeploymentCandidate) (startedDeploymentCandidate, error) {
	if len(candidates) == 0 {
		return startedDeploymentCandidate{}, errors.New("no deployment candidates were supplied")
	}
	selected := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.hasStarted != selected.hasStarted {
			if candidate.hasStarted {
				selected = candidate
			}
			continue
		}
		if candidate.deployment.ID < selected.deployment.ID {
			selected = candidate
		}
	}
	return selected, nil
}

type doraStatusHistory struct {
	startedID             int64
	exposureID            int64
	completionID          int64
	completionDescription string
	completionResult      string
}

func reconcileStatus(statuses []githubapi.DeploymentStatus, environment string, productionTraffic bool, desiredState, desiredDescription string) (int64, bool, error) {
	history := doraStatusHistory{}
	doraStatuses := make([]githubapi.DeploymentStatus, 0, len(statuses))
	for _, status := range statuses {
		if !strings.HasPrefix(status.Description, "dora5:") {
			continue
		}
		if status.CreatedAt.IsZero() {
			return 0, false, fmt.Errorf("deployment status %d is missing created_at", status.ID)
		}
		doraStatuses = append(doraStatuses, status)
	}
	sort.Slice(doraStatuses, func(left, right int) bool {
		if doraStatuses[left].CreatedAt.Equal(doraStatuses[right].CreatedAt) {
			return doraStatuses[left].ID < doraStatuses[right].ID
		}
		return doraStatuses[left].CreatedAt.Before(doraStatuses[right].CreatedAt)
	})
	for _, status := range doraStatuses {
		fact, err := event.ParseStatusDescription(status.Description)
		if err != nil {
			return 0, false, fmt.Errorf("deployment status %d has an invalid DORA 5 description: %w", status.ID, err)
		}
		if status.Environment != environment {
			return 0, false, fmt.Errorf("deployment status %d uses environment %q, not %q", status.ID, status.Environment, environment)
		}
		expectedState := ""
		switch fact.Kind {
		case event.StatusStarted:
			expectedState = "queued"
			if history.exposureID != 0 || history.completionID != 0 {
				return 0, false, errors.New("deployment has a rollout-start status recorded after a later DORA 5 status")
			}
			if history.startedID == 0 {
				history.startedID = status.ID
			}
		case event.StatusExposure:
			expectedState = "in_progress"
			if !productionTraffic {
				return 0, false, errors.New("deployment that does not serve production traffic has a production-exposure status")
			}
			if history.startedID == 0 {
				return 0, false, errors.New("deployment has a production-exposure status without an earlier rollout-start status")
			}
			if history.completionID != 0 {
				return 0, false, errors.New("deployment has a production-exposure status recorded after a terminal status")
			}
			if history.exposureID == 0 {
				history.exposureID = status.ID
			}
		case event.StatusCompleted:
			expectedState, err = deploymentState(fact.Result)
			if err != nil {
				return 0, false, err
			}
			if history.startedID == 0 {
				return 0, false, errors.New("deployment has a terminal status without an earlier rollout-start status")
			}
			if history.completionID != 0 && history.completionDescription != status.Description {
				return 0, false, errors.New("deployment has conflicting terminal DORA 5 statuses")
			}
			if history.completionID == 0 {
				history.completionID = status.ID
				history.completionDescription = status.Description
				history.completionResult = fact.Result
			}
		}
		if status.State != expectedState {
			return 0, false, fmt.Errorf("deployment status %d uses state %q, not %q", status.ID, status.State, expectedState)
		}
	}
	if history.completionID != 0 {
		completion, err := event.ParseStatusDescription(history.completionDescription)
		if err != nil {
			return 0, false, err
		}
		if productionTraffic && completion.ReleaseChanged != nil && *completion.ReleaseChanged && history.exposureID == 0 {
			return 0, false, errors.New("terminal release-changed=true lacks a production-exposure status")
		}
		if completion.ReleaseChanged != nil && !*completion.ReleaseChanged && history.exposureID != 0 {
			return 0, false, errors.New("terminal release-changed=false conflicts with a production-exposure status")
		}
	}

	desired, err := event.ParseStatusDescription(desiredDescription)
	if err != nil {
		return 0, false, err
	}
	expectedDesiredState := ""
	switch desired.Kind {
	case event.StatusStarted:
		expectedDesiredState = "queued"
	case event.StatusExposure:
		if !productionTraffic {
			return 0, false, errors.New("cannot expose a deployment that does not serve production traffic")
		}
		expectedDesiredState = "in_progress"
	case event.StatusCompleted:
		expectedDesiredState, err = deploymentState(desired.Result)
		if err != nil {
			return 0, false, err
		}
	}
	if desiredState != expectedDesiredState {
		return 0, false, errors.New("requested GitHub state does not match the DORA 5 status fact")
	}
	switch desired.Kind {
	case event.StatusStarted:
		if history.completionID != 0 && history.completionResult != "success" {
			return 0, false, errors.New("cannot reuse a rollout after a non-success terminal result; use a new rollout-group-key")
		}
		if history.startedID != 0 {
			return history.startedID, true, nil
		}
		if history.exposureID != 0 || history.completionID != 0 {
			return 0, false, errors.New("cannot record rollout start after a later DORA 5 status")
		}
	case event.StatusExposure:
		if history.exposureID != 0 {
			return history.exposureID, true, nil
		}
		if history.completionID != 0 {
			return 0, false, errors.New("cannot expose a terminal deployment")
		}
		if history.startedID == 0 {
			return 0, false, errors.New("cannot expose a deployment without a rollout-start status")
		}
	case event.StatusCompleted:
		if history.completionID != 0 {
			if history.completionDescription == desiredDescription {
				return history.completionID, true, nil
			}
			return 0, false, errors.New("cannot replace a terminal DORA 5 status")
		}
		if history.startedID == 0 {
			return 0, false, errors.New("cannot complete a deployment without a rollout-start status")
		}
		if productionTraffic && desired.ReleaseChanged != nil && *desired.ReleaseChanged && history.exposureID == 0 {
			return 0, false, errors.New("release-changed=true requires a production-exposure status before completion")
		}
		if desired.ReleaseChanged != nil && !*desired.ReleaseChanged && history.exposureID != 0 {
			return 0, false, errors.New("release-changed=false conflicts with an existing production-exposure status")
		}
	}
	return 0, false, nil
}

func deploymentState(result string) (string, error) {
	switch result {
	case "success":
		return "success", nil
	case "failure":
		return "failure", nil
	case "cancelled", "skipped":
		return "inactive", nil
	case "error":
		return "error", nil
	default:
		return "", errors.New("result must be success, failure, cancelled, skipped, or error")
	}
}

func eventIdentity(repository string, runID int64, job, rollout, serviceID, targetID, targetSetID, environment string) string {
	digest := sha256.New()
	for _, component := range []string{repository, strconv.FormatInt(runID, 10), job, rollout, serviceID, targetID, targetSetID, environment} {
		digest.Write([]byte(strconv.Itoa(len(component))))
		digest.Write([]byte{':'})
		digest.Write([]byte(component))
	}
	return "v1:sha256:" + hex.EncodeToString(digest.Sum(nil))
}

func splitValues(raw string) []string {
	var result []string
	seen := make(map[string]struct{})
	for _, value := range strings.Split(raw, ",") {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			result = append(result, key)
		}
	}
	sort.Strings(result)
	return result
}

func waitMutationInterval(ctx context.Context) error {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func writeOutputs(values map[string]string) error {
	for name, value := range values {
		if err := writeGitHubOutput(name, value); err != nil {
			return err
		}
	}
	return nil
}

func writeGitHubOutput(name, value string) error {
	path := os.Getenv("GITHUB_OUTPUT")
	if path == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open GITHUB_OUTPUT: %w", err)
	}
	if _, err := fmt.Fprintf(file, "%s=%s\n", name, value); err != nil {
		file.Close()
		return fmt.Errorf("write GITHUB_OUTPUT: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close GITHUB_OUTPUT: %w", err)
	}
	return nil
}
