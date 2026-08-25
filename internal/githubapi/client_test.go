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
