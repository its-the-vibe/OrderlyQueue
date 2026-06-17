package models

type PoppitMergeCommand struct {
	Repo     string            `json:"repo"`
	Branch   string            `json:"branch"`
	Type     string            `json:"type"`
	Dir      string            `json:"dir"`
	Commands []string          `json:"commands"`
	Metadata PoppitMetadata    `json:"metadata"`
}

type PoppitMetadata struct {
	PRURL string `json:"pr_url"`
}

type GithubPREvent struct {
	Action         string `json:"action"`
	PRURL          string `json:"pr_url"`
	State          string `json:"state"`
	Merged         bool   `json:"merged"`
	MergeCommitSHA string `json:"merge_commit_sha"`
}

type CICDCompletionEvent struct {
	CorrelationID string `json:"correlation_id"`
	Event         string `json:"event"`
	Timestamp     string `json:"timestamp"`
}
