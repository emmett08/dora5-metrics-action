// Package githubapi implements the action's narrow mutation-only REST client.
package githubapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const DefaultBaseURL = "https://api.github.com"

type Client struct {
	baseURL    string
	token      string
	apiVersion string
	version    string
	httpClient *http.Client
}

func NewClient(baseURL, token, apiVersion, version string, timeout time.Duration) (*Client, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("GitHub token is required")
	}
	if !strings.HasPrefix(baseURL, "https://") && !strings.HasPrefix(baseURL, "http://") {
		return nil, errors.New("GitHub API base URL must use HTTP or HTTPS")
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), token: token, apiVersion: apiVersion, version: version, httpClient: &http.Client{Timeout: timeout}}, nil
}

func (client *Client) doJSON(ctx context.Context, method, path string, input, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode GitHub request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+"/"+strings.TrimLeft(path, "/"), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create GitHub request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("X-GitHub-Api-Version", client.apiVersion)
	request.Header.Set("User-Agent", "dora5-metrics-action/"+client.version)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("GitHub API %s %s: %w", method, path, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		errorBody, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return fmt.Errorf("GitHub API %s %s returned %s: %s", method, path, response.Status, strings.TrimSpace(string(errorBody)))
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output); err != nil {
		return fmt.Errorf("decode GitHub API response for %s: %w", path, err)
	}
	return nil
}
