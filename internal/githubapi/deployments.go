package githubapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/emmett08/dora5-metrics-action/event"
)

const (
	pageSize     = 100
	maxListPages = 10
)

type CreateDeploymentRequest struct {
	Ref                   string        `json:"ref"`
	Task                  string        `json:"task"`
	AutoMerge             bool          `json:"auto_merge"`
	RequiredContexts      []string      `json:"required_contexts"`
	Payload               event.Payload `json:"payload"`
	Environment           string        `json:"environment"`
	Description           string        `json:"description,omitempty"`
	TransientEnvironment  bool          `json:"transient_environment"`
	ProductionEnvironment bool          `json:"production_environment"`
}

type CreateDeploymentStatusRequest struct {
	State          string `json:"state"`
	LogURL         string `json:"log_url,omitempty"`
	Description    string `json:"description,omitempty"`
	Environment    string `json:"environment,omitempty"`
	EnvironmentURL string `json:"environment_url,omitempty"`
	AutoInactive   bool   `json:"auto_inactive"`
}

type identifier struct {
	ID int64 `json:"id"`
}

type Deployment struct {
	ID          int64
	SHA         string
	Environment string
	Payload     event.Payload
}

type DeploymentStatus struct {
	ID          int64
	State       string
	Description string
	Environment string
	CreatedAt   time.Time
}

func (client *Client) CreateDeployment(ctx context.Context, repository string, request CreateDeploymentRequest) (int64, error) {
	var result identifier
	if err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/deployments", repository), request, &result); err != nil {
		return 0, err
	}
	if result.ID <= 0 {
		return 0, errors.New("GitHub deployment response did not contain a positive ID")
	}
	return result.ID, nil
}

func (client *Client) CreateDeploymentStatus(ctx context.Context, repository string, deploymentID int64, request CreateDeploymentStatusRequest) (int64, error) {
	var result identifier
	if err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/deployments/%d/statuses", repository, deploymentID), request, &result); err != nil {
		return 0, err
	}
	if result.ID <= 0 {
		return 0, errors.New("GitHub deployment-status response did not contain a positive ID")
	}
	return result.ID, nil
}

func (client *Client) ListDORADeployments(ctx context.Context, repository, commitSHA, environment string) ([]Deployment, error) {
	query := url.Values{
		"environment": {environment},
		"sha":         {commitSHA},
		"task":        {event.Task},
		"per_page":    {strconv.Itoa(pageSize)},
	}
	matches := make([]Deployment, 0, 1)
	for page := 1; page <= maxListPages; page++ {
		query.Set("page", strconv.Itoa(page))
		deployments := make([]apiDeployment, 0)
		path := fmt.Sprintf("/repos/%s/deployments?%s", repository, query.Encode())
		if err := client.doJSON(ctx, http.MethodGet, path, nil, &deployments); err != nil {
			return nil, err
		}
		if deployments == nil {
			return nil, errors.New("GitHub deployment list response was null")
		}
		for _, deployment := range deployments {
			if deployment.ID <= 0 || deployment.SHA != commitSHA || deployment.Task != event.Task || deployment.Environment != environment {
				return nil, errors.New("GitHub deployment list contained a record outside the requested scope")
			}
			var header struct {
				EventVersion string `json:"event_version"`
			}
			if err := json.Unmarshal(deployment.Payload, &header); err != nil {
				return nil, fmt.Errorf("decode deployment %d payload header: %w", deployment.ID, err)
			}
			if header.EventVersion != event.Version {
				continue
			}
			payload, err := event.Decode(deployment.Payload)
			if err != nil {
				return nil, fmt.Errorf("decode existing deployment %d: %w", deployment.ID, err)
			}
			matches = append(matches, Deployment{ID: deployment.ID, SHA: deployment.SHA, Environment: deployment.Environment, Payload: payload})
		}
		if len(deployments) < pageSize {
			return matches, nil
		}
	}
	return nil, errors.New("GitHub deployment reconciliation exceeded 1,000 matching records")
}

func (client *Client) GetDORADeployment(ctx context.Context, repository string, deploymentID int64) (Deployment, error) {
	var deployment apiDeployment
	path := fmt.Sprintf("/repos/%s/deployments/%d", repository, deploymentID)
	if err := client.doJSON(ctx, http.MethodGet, path, nil, &deployment); err != nil {
		return Deployment{}, err
	}
	if deployment.ID != deploymentID {
		return Deployment{}, errors.New("GitHub deployment response contained an unexpected ID")
	}
	if deployment.Task != event.Task {
		return Deployment{}, fmt.Errorf("deployment %d has task %q, not %q", deploymentID, deployment.Task, event.Task)
	}
	payload, err := event.Decode(deployment.Payload)
	if err != nil {
		return Deployment{}, fmt.Errorf("decode deployment %d: %w", deploymentID, err)
	}
	return Deployment{ID: deployment.ID, SHA: deployment.SHA, Environment: deployment.Environment, Payload: payload}, nil
}

func (client *Client) ListDeploymentStatuses(ctx context.Context, repository string, deploymentID int64) ([]DeploymentStatus, error) {
	result := make([]DeploymentStatus, 0, 3)
	for page := 1; page <= maxListPages; page++ {
		query := url.Values{
			"page":     {strconv.Itoa(page)},
			"per_page": {strconv.Itoa(pageSize)},
		}
		statuses := make([]apiDeploymentStatus, 0)
		path := fmt.Sprintf("/repos/%s/deployments/%d/statuses?%s", repository, deploymentID, query.Encode())
		if err := client.doJSON(ctx, http.MethodGet, path, nil, &statuses); err != nil {
			return nil, err
		}
		if statuses == nil {
			return nil, errors.New("GitHub deployment-status list response was null")
		}
		for _, status := range statuses {
			if status.ID <= 0 || status.CreatedAt.IsZero() {
				return nil, errors.New("GitHub deployment-status response contained a non-positive ID or missing created_at")
			}
			result = append(result, DeploymentStatus{ID: status.ID, State: status.State, Description: status.Description, Environment: status.Environment, CreatedAt: status.CreatedAt})
		}
		if len(statuses) < pageSize {
			return result, nil
		}
	}
	return nil, errors.New("GitHub deployment-status reconciliation exceeded 1,000 records")
}
