package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/emmett08/dora5-metrics-action/event"
	"github.com/emmett08/dora5-metrics-action/internal/githubapi"
)

const (
	actionVersion    = "1.1.0"
	githubAPIVersion = "2022-11-28"
)

func emitCommand(ctx context.Context, arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("emit", flag.ContinueOnError)
	flags.SetOutput(stderr)
	eventName := flags.String("event", "", "started, exposed, or completed")
	deploymentID := flags.Int64("deployment-id", 0, "GitHub deployment ID")
	environment := flags.String("environment", "", "raw GitHub environment name")
	jobName := flags.String("job-name", "", "REST workflow-job display name")
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
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if err := validateOptionalHTTPURL("log-url", *logURL); err != nil {
		return err
	}
	if err := validateOptionalHTTPURL("environment-url", *environmentURL); err != nil {
		return err
	}

	runtime, err := loadRuntimeIdentity()
	if err != nil {
		return err
	}
	recordedJobName := runtime.JobName
	if *jobName != "" {
		if err := validateTextInput("job-name", *jobName); err != nil {
			return err
		}
		recordedJobName = *jobName
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
			EventVersion: event.Version, EventID: eventIdentity(runtime.Repository, runtime.RunID, recordedJobName, *rolloutKey, *serviceID, *targetID, *targetSetID, *environment),
			SourceSystem: "github_actions", Repository: runtime.Repository, WorkflowPath: runtime.WorkflowPath,
			RunID: runtime.RunID, RunAttempt: runtime.RunAttempt, JobName: recordedJobName, CommitSHA: runtime.CommitSHA, RefName: runtime.RefName,
			RawEnvironmentName: *environment, ReleaseUnitRef: *releaseUnit, RolloutGroupKey: *rolloutKey, TargetSetID: *targetSetID,
			DeliveryModel: "github_actions", EventTimeUTC: time.Now().UTC(), ManualInvocation: runtime.ManualInvocation,
			ServiceID: *serviceID, TargetID: *targetID, ProductionTraffic: &productionTraffic, FinalStage: &finalStage,
			ChangeSHAs: splitValues(*changeSHAsRaw), ChangeRelationSource: *changeRelationSource, WorkType: *workType, WorkReason: *workReason,
		}
		if err := event.Validate(payload); err != nil {
			return err
		}
		deployments, err := api.ListDORADeployments(ctx, runtime.Repository, runtime.CommitSHA, *environment)
		if err != nil {
			return err
		}
		matches := make([]githubapi.Deployment, 0, 1)
		for _, deployment := range deployments {
			logicalMatch := sameLogicalStage(deployment.Payload, payload)
			if deployment.Payload.EventID == payload.EventID && !logicalMatch {
				return errors.New("an existing deployment has the requested event-id but identifies a different rollout stage")
			}
			if !logicalMatch {
				continue
			}
			if !sameEventIntent(deployment.Payload, payload) {
				return errors.New("an existing deployment identifies the same rollout stage but contains different rollout facts")
			}
			matches = append(matches, deployment)
		}
		var id int64
		var statusID int64
		var statusExists bool
		created := len(matches) == 0
		if !created {
			candidates := make([]startedDeploymentCandidate, 0, len(matches))
			for _, deployment := range matches {
				statuses, listErr := api.ListDeploymentStatuses(ctx, runtime.Repository, deployment.ID)
				if listErr != nil {
					return listErr
				}
				candidateStatusID, candidateHasStarted, reconcileErr := reconcileStatus(statuses, *environment, *deployment.Payload.ProductionTraffic, "queued", event.StartedDescription)
				if reconcileErr != nil {
					return fmt.Errorf("deployment %d cannot be reconciled: %w", deployment.ID, reconcileErr)
				}
				candidates = append(candidates, startedDeploymentCandidate{
					deployment: deployment,
					statusID:   candidateStatusID,
					hasStarted: candidateHasStarted,
				})
			}
			selected, selectErr := selectStartedDeployment(candidates)
			if selectErr != nil {
				return selectErr
			}
			payload.EventID = selected.deployment.Payload.EventID
			id = selected.deployment.ID
			statusID = selected.statusID
			statusExists = selected.hasStarted
		}
		if err := writeOutputs(map[string]string{"event-id": payload.EventID}); err != nil {
			return err
		}
		if created {
			id, err = api.CreateDeployment(ctx, runtime.Repository, githubapi.CreateDeploymentRequest{
				Ref: runtime.CommitSHA, Task: event.Task, AutoMerge: false, RequiredContexts: []string{}, Payload: payload,
				Environment: *environment, Description: "DORA 5 rollout", ProductionEnvironment: productionTraffic,
			})
			if err != nil {
				return err
			}
		}
		if err := writeOutputs(map[string]string{"deployment-id": strconv.FormatInt(id, 10)}); err != nil {
			return err
		}

		if !statusExists {
			if err := waitMutationInterval(ctx); err != nil {
				return err
			}
			statusID, err = api.CreateDeploymentStatus(ctx, runtime.Repository, id, githubapi.CreateDeploymentStatusRequest{
				State: "queued", Description: event.StartedDescription, Environment: *environment, EnvironmentURL: *environmentURL, LogURL: *logURL, AutoInactive: false,
			})
			if err != nil {
				return err
			}
		}
		if err := writeOutputs(map[string]string{"status-id": strconv.FormatInt(statusID, 10)}); err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(map[string]any{"deployment_id": id, "status_id": statusID, "event_id": payload.EventID})

	case "exposed", "completed":
		if *deploymentID <= 0 {
			return errors.New("deployment-id must be a positive integer")
		}
		if err := validateTextInput("environment", *environment); err != nil {
			return err
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
		deployment, err := api.GetDORADeployment(ctx, runtime.Repository, *deploymentID)
		if err != nil {
			return err
		}
		if err := validateDeploymentIdentity(deployment, runtime, *environment, recordedJobName); err != nil {
			return err
		}
		statuses, err := api.ListDeploymentStatuses(ctx, runtime.Repository, *deploymentID)
		if err != nil {
			return err
		}
		statusID, exists, err := reconcileStatus(statuses, *environment, *deployment.Payload.ProductionTraffic, state, description)
		if err != nil {
			return err
		}
		if exists {
			if err := writeOutputs(map[string]string{"status-id": strconv.FormatInt(statusID, 10)}); err != nil {
				return err
			}
			return json.NewEncoder(stdout).Encode(map[string]any{"deployment_id": *deploymentID, "status_id": statusID})
		}
		// GitHub recommends serial mutation requests with at least one second
		// between them to avoid secondary rate limits. Each action invocation
		// observes that boundary even when the preceding step just completed.
		if err := waitMutationInterval(ctx); err != nil {
			return err
		}
		statusID, err = api.CreateDeploymentStatus(ctx, runtime.Repository, *deploymentID, githubapi.CreateDeploymentStatusRequest{
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
