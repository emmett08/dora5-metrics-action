// Package githubapi implements the action's narrow GitHub Deployments REST client.
package githubapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBaseURL  = "https://api.github.com"
	maxReadAttempts = 3
	maxRetryDelay   = 30 * time.Second
	maxServerDelay  = 2 * time.Minute
)

var errUnsafeRedirect = errors.New("refusing unsafe GitHub API redirect")

type Client struct {
	baseURL    string
	token      string
	apiVersion string
	version    string
	httpClient *http.Client
}

func NewClient(baseURL, token, apiVersion, version string, timeout time.Duration) (*Client, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("GitHub token is required")
	}
	if strings.ContainsAny(token, "\x00\r\n") {
		return nil, errors.New("GitHub token contains a control character")
	}
	parsedBaseURL, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsedBaseURL.Host == "" || parsedBaseURL.User != nil || parsedBaseURL.RawQuery != "" || parsedBaseURL.Fragment != "" {
		return nil, errors.New("GitHub API base URL must be an absolute URL without credentials, query, or fragment")
	}
	if parsedBaseURL.Scheme != "https" && !(parsedBaseURL.Scheme == "http" && loopbackHost(parsedBaseURL.Hostname())) {
		return nil, errors.New("GitHub API base URL must use HTTPS")
	}
	httpClient := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errUnsafeRedirect
			}
			if request.URL.Scheme != parsedBaseURL.Scheme || !strings.EqualFold(request.URL.Host, parsedBaseURL.Host) {
				return errUnsafeRedirect
			}
			return nil
		},
	}
	return &Client{baseURL: strings.TrimRight(parsedBaseURL.String(), "/"), token: token, apiVersion: apiVersion, version: version, httpClient: httpClient}, nil
}

func (client *Client) doJSON(ctx context.Context, method, path string, input, output any) error {
	var encodedBody []byte
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode GitHub request: %w", err)
		}
		encodedBody = encoded
	}
	attempts := 1
	if method == http.MethodGet {
		attempts = maxReadAttempts
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		var body io.Reader
		if encodedBody != nil {
			body = bytes.NewReader(encodedBody)
		}
		request, err := http.NewRequestWithContext(ctx, method, client.baseURL+"/"+strings.TrimLeft(path, "/"), body)
		if err != nil {
			return fmt.Errorf("create GitHub request: %w", err)
		}
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("Authorization", "Bearer "+client.token)
		request.Header.Set("X-GitHub-Api-Version", client.apiVersion)
		request.Header.Set("User-Agent", "dora5-metrics-action/"+client.version)
		if input != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := client.httpClient.Do(request)
		if err != nil {
			if errors.Is(err, errUnsafeRedirect) {
				return fmt.Errorf("GitHub API %s %s: %w", method, path, err)
			}
			if attempt < attempts && ctx.Err() == nil {
				if err := waitForRetry(ctx, time.Duration(attempt)*time.Second); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("GitHub API %s %s: %w", method, path, err)
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output)
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<10))
			_ = response.Body.Close()
			if decodeErr != nil {
				return fmt.Errorf("decode GitHub API response for %s: %w", path, decodeErr)
			}
			return nil
		}
		errorBody, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		if attempt < attempts && retryableReadResponse(response, errorBody) {
			delay, retry := retryDelay(response.Header, attempt, time.Now(), secondaryRateLimited(response, errorBody))
			if !retry {
				return fmt.Errorf("GitHub API %s %s returned %s: %s", method, path, response.Status, strings.TrimSpace(string(errorBody)))
			}
			if err := waitForRetry(ctx, delay); err != nil {
				return err
			}
			continue
		}
		return fmt.Errorf("GitHub API %s %s returned %s: %s", method, path, response.Status, strings.TrimSpace(string(errorBody)))
	}
	return errors.New("GitHub API read attempts exhausted")
}

func loopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func retryableReadResponse(response *http.Response, body []byte) bool {
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return true
	}
	if response.StatusCode != http.StatusForbidden {
		return false
	}
	message := strings.ToLower(string(body))
	return response.Header.Get("Retry-After") != "" || response.Header.Get("X-RateLimit-Remaining") == "0" || strings.Contains(message, "rate limit")
}

func secondaryRateLimited(response *http.Response, body []byte) bool {
	if response.StatusCode != http.StatusForbidden && response.StatusCode != http.StatusTooManyRequests {
		return false
	}
	if response.Header.Get("X-RateLimit-Remaining") == "0" {
		return false
	}
	message := strings.ToLower(string(body))
	return strings.Contains(message, "secondary rate limit") || strings.Contains(message, "abuse detection")
}

func retryDelay(headers http.Header, attempt int, now time.Time, secondaryLimit bool) (time.Duration, bool) {
	retryAfter := strings.TrimSpace(headers.Get("Retry-After"))
	if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds >= 0 {
		delay := time.Duration(seconds) * time.Second
		return delay, delay <= maxServerDelay
	}
	if when, err := http.ParseTime(retryAfter); err == nil {
		delay := max(when.Sub(now), 0)
		return delay, delay <= maxServerDelay
	}
	if secondaryLimit {
		return min(time.Duration(attempt)*time.Minute, maxServerDelay), true
	}
	if reset, err := strconv.ParseInt(headers.Get("X-RateLimit-Reset"), 10, 64); err == nil && reset > 0 {
		delay := max(time.Unix(reset, 0).Sub(now)+time.Second, 0)
		return delay, delay <= maxServerDelay
	}
	return min(time.Duration(attempt)*time.Second, maxRetryDelay), true
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
