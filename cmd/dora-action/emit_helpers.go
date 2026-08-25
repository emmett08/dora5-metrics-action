package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/emmett08/dora5-metrics-action/event"
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

func eventIdentity(repository string, runID int64, attempt int, job, rollout string) string {
	return fmt.Sprintf("v1:%s:%d:%d:%s:%s", repository, runID, attempt, job, rollout)
}

func splitValues(raw string) []string {
	var result []string
	for _, value := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			result = append(result, trimmed)
		}
	}
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
