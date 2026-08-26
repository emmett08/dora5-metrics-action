package githubapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emmett08/dora5-metrics-action/event"
)

func TestDeploymentMutationIsIssuedOnce(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writer.Header().Set("Retry-After", "1")
		writer.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(writer, `{"message":"secondary rate limit"}`)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "token", "2022-11-28", "test", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CreateDeployment(context.Background(), "example/repo", CreateDeploymentRequest{
		Ref: strings.Repeat("a", 40), Task: event.Task, RequiredContexts: []string{}, Payload: event.Payload{},
	})
	if err == nil {
		t.Fatal("expected rate-limit error")
	}
	if calls.Load() != 1 {
		t.Fatalf("mutation calls = %d, want 1", calls.Load())
	}
}

func TestMutationSetsVersionedHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("X-GitHub-Api-Version"); got != "2022-11-28" {
			t.Errorf("API version = %q", got)
		}
		if got := request.Header.Get("User-Agent"); got != "dora5-metrics-action/test" {
			t.Errorf("user agent = %q", got)
		}
		fmt.Fprint(writer, `{"id":42}`)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "token", "2022-11-28", "test", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	id, err := client.CreateDeploymentStatus(context.Background(), "example/repo", 1, CreateDeploymentStatusRequest{State: "success"})
	if err != nil {
		t.Fatal(err)
	}
	if id != 42 {
		t.Fatalf("status ID = %d, want 42", id)
	}
}

func TestClientRejectsInsecureAPIAndMalformedToken(t *testing.T) {
	for _, test := range []struct {
		name  string
		base  string
		token string
	}{
		{name: "remote HTTP", base: "http://api.github.com", token: "token"},
		{name: "credentials in URL", base: "https://user@example.com", token: "token"},
		{name: "query in URL", base: "https://api.github.com?redirect=elsewhere", token: "token"},
		{name: "token newline", base: "https://api.github.com", token: "token\nsecond-header"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewClient(test.base, test.token, "2022-11-28", "test", time.Second); err == nil {
				t.Fatal("unsafe client configuration was accepted")
			}
		})
	}
}

func TestMutationRejectsMissingIdentifier(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(writer, `{"id":0}`)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "token", "2022-11-28", "test", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateDeploymentStatus(context.Background(), "example/repo", 1, CreateDeploymentStatusRequest{State: "success"}); err == nil {
		t.Fatal("a successful response without a positive status ID was accepted")
	}
}

func TestReadRetriesRateLimitWithoutRetryingMutation(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		call := calls.Add(1)
		if call == 1 {
			writer.Header().Set("Retry-After", "0")
			writer.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(writer, `{"message":"rate limit"}`)
			return
		}
		fmt.Fprint(writer, `{"id":42}`)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "token", "2022-11-28", "test", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var result identifier
	if err := client.doJSON(context.Background(), http.MethodGet, "/resource", nil, &result); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || result.ID != 42 {
		t.Fatalf("read calls/id = %d/%d, want 2/42", calls.Load(), result.ID)
	}
}

func TestRetryDelayHonoursServerBounds(t *testing.T) {
	now := time.Unix(1_000, 0).UTC()
	for _, test := range []struct {
		name           string
		headers        http.Header
		attempt        int
		secondaryLimit bool
		want           time.Duration
		retry          bool
	}{
		{name: "retry after", headers: http.Header{"Retry-After": {"45"}}, attempt: 2, want: 45 * time.Second, retry: true},
		{name: "retry after exceeds bound", headers: http.Header{"Retry-After": {"121"}}, attempt: 2, want: 121 * time.Second, retry: false},
		{name: "secondary limit honours retry after", headers: http.Header{"Retry-After": {"45"}}, attempt: 1, secondaryLimit: true, want: 45 * time.Second, retry: true},
		{name: "secondary limit fallback", headers: http.Header{}, attempt: 1, secondaryLimit: true, want: time.Minute, retry: true},
		{name: "secondary limit bounded backoff", headers: http.Header{}, attempt: 3, secondaryLimit: true, want: 2 * time.Minute, retry: true},
		{name: "rate reset", headers: http.Header{"X-Ratelimit-Reset": {"1060"}}, attempt: 2, want: time.Minute + time.Second, retry: true},
		{name: "local backoff", headers: http.Header{}, attempt: 2, want: 2 * time.Second, retry: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, retry := retryDelay(test.headers, test.attempt, now, test.secondaryLimit)
			if got != test.want || retry != test.retry {
				t.Fatalf("retry delay = (%s, %t), want (%s, %t)", got, retry, test.want, test.retry)
			}
		})
	}
}

func TestSecondaryRateLimitDetection(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		remaining string
		body      string
		want      bool
	}{
		{name: "secondary forbidden", status: http.StatusForbidden, remaining: "4999", body: `{"message":"You have exceeded a secondary rate limit."}`, want: true},
		{name: "secondary too many requests", status: http.StatusTooManyRequests, body: `{"message":"Secondary rate limit"}`, want: true},
		{name: "legacy abuse response", status: http.StatusForbidden, body: `{"message":"Abuse detection mechanism"}`, want: true},
		{name: "primary exhausted", status: http.StatusForbidden, remaining: "0", body: `{"message":"API rate limit exceeded"}`},
		{name: "unrelated forbidden", status: http.StatusForbidden, remaining: "4999", body: `{"message":"Resource not accessible"}`},
		{name: "server error", status: http.StatusServiceUnavailable, body: `{"message":"secondary rate limit"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := &http.Response{StatusCode: test.status, Header: make(http.Header)}
			if test.remaining != "" {
				response.Header.Set("X-RateLimit-Remaining", test.remaining)
			}
			if got := secondaryRateLimited(response, []byte(test.body)); got != test.want {
				t.Fatalf("secondaryRateLimited() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestClientDoesNotRedirectCredentialsAcrossOrigins(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		targetCalls.Add(1)
		fmt.Fprint(writer, `{"id":42}`)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", target.URL)
		writer.WriteHeader(http.StatusFound)
	}))
	defer source.Close()
	client, err := NewClient(source.URL, "sensitive-token", "2022-11-28", "test", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.doJSON(context.Background(), http.MethodGet, "/redirect", nil, &identifier{}); err == nil {
		t.Fatal("cross-origin redirect was accepted")
	}
	if targetCalls.Load() != 0 {
		t.Fatal("redirect target received the credential-bearing request")
	}
}
