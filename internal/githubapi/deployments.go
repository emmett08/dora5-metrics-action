package githubapi

import (
	"context"
	"fmt"
	"net/http"

	"github.com/emmett08/dora5-metrics-action/event"
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

func (client *Client) CreateDeployment(ctx context.Context, repository string, request CreateDeploymentRequest) (int64, error) {
	var result identifier
	if err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/deployments", repository), request, &result); err != nil {
		return 0, err
	}
	return result.ID, nil
}

func (client *Client) CreateDeploymentStatus(ctx context.Context, repository string, deploymentID int64, request CreateDeploymentStatusRequest) (int64, error) {
	var result identifier
	if err := client.doJSON(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/deployments/%d/statuses", repository, deploymentID), request, &result); err != nil {
		return 0, err
	}
	return result.ID, nil
}
