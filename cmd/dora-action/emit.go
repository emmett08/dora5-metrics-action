package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/emmett08/dora5-metrics-action/event"
	"github.com/emmett08/dora5-metrics-action/internal/githubapi"
)

const (
	actionVersion    = "1.0.0"
	githubAPIVersion = "2022-11-28"
)

func emitCommand(ctx context.Context, arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("emit", flag.ContinueOnError)
	flags.SetOutput(stderr)
	eventName := flags.String("event", "", "started, exposed, or completed")
	deploymentID := flags.Int64("deployment-id", 0, "GitHub deployment ID")
	environment := flags.String("environment", "", "raw GitHub environment name")
	releaseUnit := flags.String("release-unit-ref", "", "immutable release digest")
	rolloutKey := flags.String("rollout-group-key", "", "stable rollout identifier")
	serviceID := flags.String("service-id", "", "stable service ID")
	targetID := flags.String("target-id", "", "stable target ID")
	targetSetID := flags.String("target-set-id", "", "stable target-set ID")
	productionTrafficRaw := flags.String("production-traffic", "", "true when the target serves production traffic")
	finalStageRaw := flags.String("final-stage", "", "true when this is a configured final stage")
	releaseChangedRaw := flags.String("release-changed", "", "verified true or false for exposure/completion")
	changeSHAsRaw := flags.String("change-shas", "", "comma-separated original source commit SHAs")
	changeRelationSource := flags.String("change-relation-source", "", "source of the release-to-change relation")
	workType := flags.String("work-type", "", "planned or unplanned_remediation")
	workReason := flags.String("work-reason", "", "recorded deployment-purpose reason")
	result := flags.String("result", "", "completion result")
	logURL := flags.String("log-url", "", "workflow log URL")
	environmentURL := flags.String("environment-url", "", "deployed environment URL")
	if err := flags.Parse(arguments); err != nil {
		return err
	}

	runtime, err := loadRuntimeIdentity()
	if err != nil {
		return err
	}
	token, err := tokenFromEnvironment("GITHUB_TOKEN")
	if err != nil {
		return err
	}
	baseURL := os.Getenv("GITHUB_API_URL")
	if baseURL == "" {
		baseURL = githubapi.DefaultBaseURL
	}
	api, err := githubapi.NewClient(baseURL, token, githubAPIVersion, actionVersion, 30*time.Second)
	if err != nil {
		return err
	}

	switch *eventName {
	case "started":
		productionTraffic, err := requiredBoolean("production-traffic", *productionTrafficRaw)
		if err != nil {
			return err
		}
		finalStage, err := requiredBoolean("final-stage", *finalStageRaw)
		if err != nil {
			return err
		}
		if *rolloutKey == "" && *serviceID != "" {
			*rolloutKey = *serviceID + "-" + strconv.FormatInt(runtime.RunID, 10)
		}
		payload := event.Payload{
			EventVersion: event.Version, EventID: eventIdentity(runtime.Repository, runtime.RunID, runtime.RunAttempt, runtime.JobName, *rolloutKey),
			SourceSystem: "github_actions", Repository: runtime.Repository, WorkflowPath: runtime.WorkflowPath,
			RunID: runtime.RunID, RunAttempt: runtime.RunAttempt, JobName: runtime.JobName, CommitSHA: runtime.CommitSHA, RefName: runtime.RefName,
			RawEnvironmentName: *environment, ReleaseUnitRef: *releaseUnit, RolloutGroupKey: *rolloutKey, TargetSetID: *targetSetID,
			DeliveryModel: "github_actions", EventTimeUTC: time.Now().UTC(), ManualInvocation: runtime.ManualInvocation,
			ServiceID: *serviceID, TargetID: *targetID, ProductionTraffic: &productionTraffic, FinalStage: &finalStage,
			ChangeSHAs: splitValues(*changeSHAsRaw), ChangeRelationSource: *changeRelationSource, WorkType: *workType, WorkReason: *workReason,
		}
		if err := event.Validate(payload); err != nil {
			return err
		}
		id, err := api.CreateDeployment(ctx, runtime.Repository, githubapi.CreateDeploymentRequest{
			Ref: runtime.CommitSHA, Task: event.Task, AutoMerge: false, RequiredContexts: []string{}, Payload: payload,
			Environment: *environment, Description: "DORA 5 rollout", ProductionEnvironment: productionTraffic,
		})
		if err != nil {
			return err
		}
		if err := waitMutationInterval(ctx); err != nil {
			return err
		}
		statusID, err := api.CreateDeploymentStatus(ctx, runtime.Repository, id, githubapi.CreateDeploymentStatusRequest{
			State: "queued", Description: event.StartedDescription, Environment: *environment, LogURL: *logURL, AutoInactive: false,
		})
		if err != nil {
			return err
		}
		if err := writeOutputs(map[string]string{"deployment-id": strconv.FormatInt(id, 10), "status-id": strconv.FormatInt(statusID, 10), "event-id": payload.EventID}); err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(map[string]any{"deployment_id": id, "status_id": statusID, "event_id": payload.EventID})

	case "exposed", "completed":
		if *deploymentID <= 0 || *environment == "" {
			return errors.New("deployment-id and environment are required")
		}
		state := "in_progress"
		description := ""
		if *eventName == "exposed" {
			changed, err := requiredBoolean("release-changed", *releaseChangedRaw)
			if err != nil {
				return err
			}
			if !changed {
				return errors.New("an exposure event must report release-changed=true; emit only completion for a no-op")
			}
			description = event.ExposureDescription(true)
		} else {
			state, err = deploymentState(*result)
			if err != nil {
				return err
			}
			changed, err := optionalBoolean("release-changed", *releaseChangedRaw)
			if err != nil {
				return err
			}
			description = event.CompletionDescription(*result, changed)
		}
		// GitHub recommends serial mutation requests with at least one second
		// between them to avoid secondary rate limits. Each action invocation
		// observes that boundary even when the preceding step just completed.
		if err := waitMutationInterval(ctx); err != nil {
			return err
		}
		statusID, err := api.CreateDeploymentStatus(ctx, runtime.Repository, *deploymentID, githubapi.CreateDeploymentStatusRequest{
			State: state, Description: description, Environment: *environment, EnvironmentURL: *environmentURL, LogURL: *logURL, AutoInactive: false,
		})
		if err != nil {
			return err
		}
		if err := writeOutputs(map[string]string{"status-id": strconv.FormatInt(statusID, 10)}); err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(map[string]any{"deployment_id": *deploymentID, "status_id": statusID})
	default:
		return errors.New("event must be started, exposed, or completed")
	}
}
