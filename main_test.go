package main

import (
	"context"
	"encoding/json"
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
	cfg.Channels.GithubEvents = "test:github-events"
	cfg.Channels.CICDEvents = "test:cicd-events"
	cfg.Timeouts.LockExpiry = 30 * time.Minute

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

	prURL := "http://github.com/org/repo/pull/1"
	mr.RPush(svc.cfg.Keys.PRList, prURL)

	fetched, err := svc.FetchNextPR(ctx)
	if err != nil || fetched != prURL {
		t.Errorf("expected %s, got %s, err=%v", prURL, fetched, err)
	}
}

func TestDispatchMerge(t *testing.T) {
	svc, mr := setupTestService(t)
	ctx := context.Background()

	prURL := "http://github.com/org/repo/pull/1"
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

	if cmd.Metadata.PRURL != prURL {
		t.Errorf("expected PR URL %s, got %s", prURL, cmd.Metadata.PRURL)
	}
}

func TestWaitEvents(t *testing.T) {
	svc, mr := setupTestService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	prURL := "http://github.com/org/repo/pull/1"
	mergeSHA := "deadbeef"

	// Test WaitForMergeEvent
	go func() {
		time.Sleep(100 * time.Millisecond)
		event := models.GithubPREvent{
			PRURL:          prURL,
			State:          "closed",
			Merged:         true,
			MergeCommitSHA: mergeSHA,
		}
		payload, _ := json.Marshal(event)
		mr.Publish(svc.cfg.Channels.GithubEvents, string(payload))
	}()

	sha, err := svc.WaitForMergeEvent(ctx, prURL)
	if err != nil || sha != mergeSHA {
		t.Errorf("expected SHA %s, got %s, err=%v", mergeSHA, sha, err)
	}

	// Test WaitForCICDEvent
	go func() {
		time.Sleep(100 * time.Millisecond)
		event := models.CICDCompletionEvent{
			CorrelationID: mergeSHA,
			Event:         "end",
			Timestamp:     time.Now().Format(time.RFC3339),
		}
		payload, _ := json.Marshal(event)
		mr.Publish(svc.cfg.Channels.CICDEvents, string(payload))
	}()

	err = svc.WaitForCICDEvent(ctx, mergeSHA)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}
