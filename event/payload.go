// Package event defines the public, versioned deployment-event contract.
package event

import "time"

const (
	Version = "1"
	Task    = "dora5-rollout"
)

// Payload records workflow-known rollout facts without inferring DORA meaning.
// The downstream collector validates these claims against independently
// collected GitHub Actions and service-registry evidence.
type Payload struct {
	EventVersion         string    `json:"event_version" yaml:"event_version"`
	EventID              string    `json:"event_id" yaml:"event_id"`
	SourceSystem         string    `json:"source_system" yaml:"source_system"`
	Repository           string    `json:"repository" yaml:"repository"`
	WorkflowPath         string    `json:"workflow_path" yaml:"workflow_path"`
	RunID                int64     `json:"run_id" yaml:"run_id"`
	RunAttempt           int       `json:"run_attempt" yaml:"run_attempt"`
	JobName              string    `json:"job_name" yaml:"job_name"`
	CommitSHA            string    `json:"commit_sha" yaml:"commit_sha"`
	RefName              string    `json:"ref_name" yaml:"ref_name"`
	RawEnvironmentName   string    `json:"raw_environment_name" yaml:"raw_environment_name"`
	ReleaseUnitRef       string    `json:"release_unit_ref,omitempty" yaml:"release_unit_ref,omitempty"`
	RolloutGroupKey      string    `json:"rollout_group_key" yaml:"rollout_group_key"`
	TargetSetID          string    `json:"target_set_id,omitempty" yaml:"target_set_id,omitempty"`
	DeliveryModel        string    `json:"delivery_model,omitempty" yaml:"delivery_model,omitempty"`
	EventTimeUTC         time.Time `json:"event_time_utc" yaml:"event_time_utc"`
	ManualInvocation     bool      `json:"manual_invocation" yaml:"manual_invocation"`
	ServiceID            string    `json:"service_id,omitempty" yaml:"service_id,omitempty"`
	TargetID             string    `json:"target_id,omitempty" yaml:"target_id,omitempty"`
	ProductionTraffic    *bool     `json:"production_traffic,omitempty" yaml:"production_traffic,omitempty"`
	FinalStage           *bool     `json:"final_stage,omitempty" yaml:"final_stage,omitempty"`
	ReleaseChanged       *bool     `json:"release_changed,omitempty" yaml:"release_changed,omitempty"`
	ChangeSHAs           []string  `json:"change_shas,omitempty" yaml:"change_shas,omitempty"`
	ChangeRelationSource string    `json:"change_relation_source,omitempty" yaml:"change_relation_source,omitempty"`
	WorkType             string    `json:"work_type,omitempty" yaml:"work_type,omitempty"`
	WorkReason           string    `json:"work_reason,omitempty" yaml:"work_reason,omitempty"`
}
