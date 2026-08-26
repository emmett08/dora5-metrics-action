package event

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

//go:embed schema/deployment-payload-v1.schema.json
var Schema []byte

func Decode(payload []byte) (Payload, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var decoded Payload
	if err := decoder.Decode(&decoded); err != nil {
		return Payload{}, fmt.Errorf("decode DORA 5 payload: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Payload{}, errors.New("decode DORA 5 payload: trailing JSON value")
		}
		return Payload{}, fmt.Errorf("decode DORA 5 payload trailer: %w", err)
	}
	if err := Validate(decoded); err != nil {
		return Payload{}, err
	}
	return decoded, nil
}

func Validate(payload Payload) error {
	missing := make([]string, 0)
	values := map[string]string{
		"event_id":             payload.EventID,
		"repository":           payload.Repository,
		"workflow_path":        payload.WorkflowPath,
		"job_name":             payload.JobName,
		"raw_environment_name": payload.RawEnvironmentName,
		"release_unit_ref":     payload.ReleaseUnitRef,
		"rollout_group_key":    payload.RolloutGroupKey,
		"service_id":           payload.ServiceID,
		"target_id":            payload.TargetID,
		"target_set_id":        payload.TargetSetID,
	}
	for field, value := range values {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, field)
		}
		if strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("%s contains a control character", field)
		}
	}
	if payload.EventVersion != Version {
		return fmt.Errorf("event_version must be %q", Version)
	}
	if payload.SourceSystem != "github_actions" || payload.DeliveryModel != "github_actions" {
		return errors.New("source_system and delivery_model must be github_actions")
	}
	if strings.Count(payload.Repository, "/") != 1 {
		return errors.New("repository must be owner/name")
	}
	if normalized := NormalizeWorkflowPath(payload.WorkflowPath); normalized != payload.WorkflowPath || !strings.HasPrefix(normalized, ".github/workflows/") || (!strings.HasSuffix(strings.ToLower(normalized), ".yml") && !strings.HasSuffix(strings.ToLower(normalized), ".yaml")) {
		return errors.New("workflow_path must be a normalized .github/workflows YAML path")
	}
	if payload.EventTimeUTC.IsZero() {
		return errors.New("event_time_utc is required")
	}
	if payload.RunID <= 0 || payload.RunAttempt <= 0 {
		missing = append(missing, "run_id/run_attempt")
	}
	if payload.ProductionTraffic == nil || payload.FinalStage == nil {
		missing = append(missing, "production_traffic/final_stage")
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing required event fields: %s", strings.Join(missing, ", "))
	}
	if !hexIdentifier(payload.CommitSHA, 40, 64) {
		return errors.New("commit_sha must be an immutable 40- or 64-character Git commit ID")
	}
	if !digestReference(payload.ReleaseUnitRef) {
		return errors.New("release_unit_ref must contain an immutable sha256 or sha512 digest")
	}
	if len(payload.ChangeSHAs) > 0 && payload.ChangeRelationSource == "" {
		return errors.New("change_relation_source is required when change_shas are supplied")
	}
	if len(payload.ChangeSHAs) == 0 && payload.ChangeRelationSource != "" {
		return errors.New("change_shas are required when change_relation_source is supplied")
	}
	if payload.ChangeRelationSource != "" && payload.ChangeRelationSource != "release_manifest" && payload.ChangeRelationSource != "direct_commit" {
		return errors.New("change_relation_source must be release_manifest or direct_commit")
	}
	seenChangeSHAs := make(map[string]struct{}, len(payload.ChangeSHAs))
	for _, sha := range payload.ChangeSHAs {
		if !hexIdentifier(sha, 40, 64) {
			return fmt.Errorf("change SHA %q is not a 40- or 64-character Git commit ID", sha)
		}
		key := strings.ToLower(sha)
		if _, exists := seenChangeSHAs[key]; exists {
			return fmt.Errorf("change SHA %q is duplicated", sha)
		}
		seenChangeSHAs[key] = struct{}{}
	}
	if payload.ChangeRelationSource == "direct_commit" && (len(payload.ChangeSHAs) != 1 || !strings.EqualFold(payload.ChangeSHAs[0], payload.CommitSHA)) {
		return errors.New("direct_commit requires exactly one change SHA equal to commit_sha")
	}
	if payload.WorkType != "" && payload.WorkType != "planned" && payload.WorkType != "unplanned_remediation" {
		return errors.New("work_type must be planned or unplanned_remediation")
	}
	if payload.WorkType != "" && strings.TrimSpace(payload.WorkReason) == "" {
		return errors.New("work_reason is required when work_type is supplied")
	}
	if payload.WorkType == "" && payload.WorkReason != "" {
		return errors.New("work_type is required when work_reason is supplied")
	}
	if strings.ContainsAny(payload.WorkReason, "\x00\r\n") {
		return errors.New("work_reason contains a control character")
	}
	return nil
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
	for _, length := range lengths {
		if len(value) != length {
			continue
		}
		for _, character := range strings.ToLower(value) {
			if !strings.ContainsRune("0123456789abcdef", character) {
				return false
			}
		}
		return true
	}
	return false
}
