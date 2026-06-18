package main

import (
	"context"
	"encoding/json"
	"log"
	"testing"
	"time"

	"OrderlyQueue/config"
	"OrderlyQueue/models"

	"github.com/alicebob/miniredis/v2"
)

func setupTestService(t *testing.T) (*Service, *miniredis.Miniredis) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}

	cfg := &config.Config{}
	cfg.Redis.Addr = mr.Addr()
	cfg.Keys.Lock = "test:lock"
	cfg.Keys.PRList = "test:pr-list"
	cfg.Keys.PoppitList = "test:poppit-list"
	cfg.Keys.MergeCommitSHA = "test:merge-commit-sha"
	cfg.Channels.GithubEvents = "test:github-events"
	cfg.Channels.CICDEvents = "test:cicd-events"
	cfg.Timeouts.LockExpiry = 30 * time.Minute
	cfg.Timeouts.CICDDelay = 5 * time.Minute

	svc := NewService(cfg)
	return svc, mr
}

func TestLockMechanism(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()

	// Initially no lock
	locked, err := svc.CheckLock(ctx)
	if err != nil || locked {
		t.Errorf("expected no lock, got locked=%v, err=%v", locked, err)
	}

	// Acquire lock
	err = svc.AcquireLock(ctx, "http://pr-url", 10*time.Second)
	if err != nil {
		t.Errorf("failed to acquire lock: %v", err)
	}

	// Check lock
	locked, err = svc.CheckLock(ctx)
	if err != nil || !locked {
		t.Errorf("expected lock, got locked=%v, err=%v", locked, err)
	}
}

func TestFetchNextPR(t *testing.T) {
	svc, mr := setupTestService(t)
	ctx := context.Background()

	prURL := "https://github.com/org/repo/pull/1"
	mr.RPush(svc.cfg.Keys.PRList, prURL)

	fetched, err := svc.FetchNextPR(ctx)
	if err != nil || fetched != prURL {
		t.Errorf("expected %s, got %s, err=%v", prURL, fetched, err)
	}
}

func TestDispatchMerge(t *testing.T) {
	svc, mr := setupTestService(t)
	ctx := context.Background()

	prURL := "https://github.com/owner/repo/pull/1"
	err := svc.DispatchMerge(ctx, prURL)
	if err != nil {
		t.Errorf("failed to dispatch merge: %v", err)
	}

	val, err := mr.Lpop(svc.cfg.Keys.PoppitList)
	if err != nil {
		t.Errorf("failed to pop from poppit list: %v", err)
	}

	var cmd models.PoppitMergeCommand
	if err := json.Unmarshal([]byte(val), &cmd); err != nil {
		t.Errorf("failed to unmarshal poppit command: %v", err)
	}

	if cmd.Repo != "owner/repo" {
		t.Errorf("expected repo owner/repo, got %s", cmd.Repo)
	}

	if cmd.Metadata.PRURL != prURL {
		t.Errorf("expected PR URL %s, got %s", prURL, cmd.Metadata.PRURL)
	}
}

func TestWaitEvents(t *testing.T) {
	svc, mr := setupTestService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	prURL := "https://github.com/org/repo/pull/1"
	mergeSHA := "deadbeef"

	// Mock current lock
	mr.Set(svc.cfg.Keys.Lock, prURL)

	// Start loops
	go svc.GithubEventLoop(ctx)
	go svc.CICDEventLoop(ctx)

	// Wait for subscription to be active
	time.Sleep(100 * time.Millisecond)

	// Test GithubEventLoop
	event := models.GithubPREvent{
		PullRequest: models.PullRequest{
			HTMLURL:        prURL,
			State:          "closed",
			Merged:         true,
			MergeCommitSHA: mergeSHA,
		},
	}
	payload, _ := json.Marshal(event)
	log.Printf("Publishing Github event: %s", string(payload))
	mr.Publish(svc.cfg.Channels.GithubEvents, string(payload))

	// Wait for SHA to be stored
	success := false
	for i := 0; i < 10; i++ {
		time.Sleep(100 * time.Millisecond)
		storedSHA, err := mr.Get(svc.cfg.Keys.MergeCommitSHA)
		if err == nil && storedSHA == mergeSHA {
			success = true
			break
		}
	}
	if !success {
		storedSHA, _ := mr.Get(svc.cfg.Keys.MergeCommitSHA)
		t.Errorf("expected SHA %s, got %s", mergeSHA, storedSHA)
	}

	// Test CICDEventLoop
	cicdEvent := models.CICDCompletionEvent{
		CorrelationID: mergeSHA,
		Event:         "end",
		Timestamp:     time.Now().Format(time.RFC3339),
	}
	cicdPayload, _ := json.Marshal(cicdEvent)
	log.Printf("Publishing CICD event: %s", string(cicdPayload))
	mr.Publish(svc.cfg.Channels.CICDEvents, string(cicdPayload))

	// Wait for lock expiry to be updated
	success = false
	for i := 0; i < 10; i++ {
		time.Sleep(100 * time.Millisecond)
		ttl := mr.TTL(svc.cfg.Keys.Lock)
		if ttl > 0 && ttl <= 5*time.Minute {
			success = true
			break
		}
	}
	if !success {
		ttl := mr.TTL(svc.cfg.Keys.Lock)
		t.Errorf("expected TTL to be around 5m, got %v", ttl)
	}
}
