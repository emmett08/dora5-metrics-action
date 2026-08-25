package githubapi

import (
	"encoding/json"
	"time"
)

type apiUser struct {
	Login string `json:"login"`
}

type apiDeployment struct {
	ID          int64           `json:"id"`
	NodeID      string          `json:"node_id"`
	Ref         string          `json:"ref"`
	SHA         string          `json:"sha"`
	Task        string          `json:"task"`
	Environment string          `json:"environment"`
	Description string          `json:"description"`
	Creator     apiUser         `json:"creator"`
	Payload     json.RawMessage `json:"payload"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type apiDeploymentStatus struct {
	ID             int64     `json:"id"`
	State          string    `json:"state"`
	Description    string    `json:"description"`
	Environment    string    `json:"environment"`
	EnvironmentURL string    `json:"environment_url"`
	LogURL         string    `json:"log_url"`
	Creator        apiUser   `json:"creator"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type apiWorkflowRuns struct {
	TotalCount int              `json:"total_count"`
	Runs       []apiWorkflowRun `json:"workflow_runs"`
}

type apiWorkflowRun struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Path         string    `json:"path"`
	Event        string    `json:"event"`
	RunNumber    int       `json:"run_number"`
	RunAttempt   int       `json:"run_attempt"`
	HeadSHA      string    `json:"head_sha"`
	HeadBranch   string    `json:"head_branch"`
	Status       string    `json:"status"`
	Conclusion   string    `json:"conclusion"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	RunStartedAt time.Time `json:"run_started_at"`
}

type apiWorkflowJobs struct {
	TotalCount int              `json:"total_count"`
	Jobs       []apiWorkflowJob `json:"jobs"`
}

type apiWorkflowJob struct {
	ID              int64             `json:"id"`
	RunID           int64             `json:"run_id"`
	RunAttempt      int               `json:"run_attempt"`
	Name            string            `json:"name"`
	Status          string            `json:"status"`
	Conclusion      string            `json:"conclusion"`
	HeadSHA         string            `json:"head_sha"`
	StartedAt       *time.Time        `json:"started_at"`
	CompletedAt     *time.Time        `json:"completed_at"`
	RunnerName      string            `json:"runner_name"`
	RunnerGroupName string            `json:"runner_group_name"`
	Steps           []apiWorkflowStep `json:"steps"`
}

type apiWorkflowStep struct {
	Name        string     `json:"name"`
	Number      int        `json:"number"`
	Status      string     `json:"status"`
	Conclusion  string     `json:"conclusion"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
}

type apiCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Author struct {
			Date time.Time `json:"date"`
		} `json:"author"`
		Committer struct {
			Date time.Time `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
	Author    *apiUser `json:"author"`
	Committer *apiUser `json:"committer"`
}

type apiPullRequest struct {
	Number         int        `json:"number"`
	State          string     `json:"state"`
	MergeCommitSHA string     `json:"merge_commit_sha"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	MergedAt       *time.Time `json:"merged_at"`
	Commits        int        `json:"commits"`
	ChangedFiles   int        `json:"changed_files"`
	Head           struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

type apiArtifacts struct {
	TotalCount int           `json:"total_count"`
	Artifacts  []apiArtifact `json:"artifacts"`
}

type apiArtifact struct {
	ID                 int64     `json:"id"`
	Name               string    `json:"name"`
	SizeInBytes        int64     `json:"size_in_bytes"`
	ArchiveDownloadURL string    `json:"archive_download_url"`
	Expired            bool      `json:"expired"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	ExpiresAt          time.Time `json:"expires_at"`
	WorkflowRun        struct {
		ID      int64  `json:"id"`
		HeadSHA string `json:"head_sha"`
	} `json:"workflow_run"`
}
