package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"OrderlyQueue/config"
	"OrderlyQueue/models"
	"os"
	"strings"

	"github.com/redis/go-redis/v9"
)

type Service struct {
	cfg   *config.Config
	redis *redis.Client
}

func NewService(cfg *config.Config) *Service {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	return &Service{
		cfg:   cfg,
		redis: rdb,
	}
}

// CheckLock checks if the lock key exists
func (s *Service) CheckLock(ctx context.Context) (bool, error) {
	val, err := s.redis.Exists(ctx, s.cfg.Keys.Lock).Result()
	if err != nil {
		return false, err
	}
	return val > 0, nil
}

// AcquireLock sets the lock key with the PR URL and expiry
func (s *Service) AcquireLock(ctx context.Context, prURL string, expiry time.Duration) error {
	return s.redis.Set(ctx, s.cfg.Keys.Lock, prURL, expiry).Err()
}

// UpdateLockExpiry updates the expiry of the existing lock
func (s *Service) UpdateLockExpiry(ctx context.Context, expiry time.Duration) error {
	return s.redis.Expire(ctx, s.cfg.Keys.Lock, expiry).Err()
}

// FetchNextPR retrieves the next PR URL from the Redis list using BLPOP
func (s *Service) FetchNextPR(ctx context.Context) (string, error) {
	// BLPOP returns [key, value]
	res, err := s.redis.BLPop(ctx, 0, s.cfg.Keys.PRList).Result()
	if err != nil {
		return "", err
	}
	if len(res) < 2 {
		return "", fmt.Errorf("unexpected BLPOP result length: %d", len(res))
	}
	return res[1], nil
}

// DispatchMerge sends the merge command to Poppit
func (s *Service) DispatchMerge(ctx context.Context, prURL string) error {
	// Parse owner/repo from PR URL: https://github.com/owner/repo/pull/1
	parts := strings.Split(prURL, "/")
	if len(parts) < 5 {
		return fmt.Errorf("invalid PR URL: %s", prURL)
	}
	repo := parts[3] + "/" + parts[4]

	cmd := models.PoppitMergeCommand{
		Repo:   repo,
		Branch: "refs/heads/main",
		Type:   "orderly-queue",
		Dir:    s.cfg.Poppit.Dir,
		Commands: []string{
			fmt.Sprintf("gh pr merge %s --squash", prURL),
		},
		Metadata: models.PoppitMetadata{
			PRURL: prURL,
		},
	}
	payload, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	return s.redis.RPush(ctx, s.cfg.Keys.PoppitList, payload).Err()
}

func (s *Service) Run(ctx context.Context) error {
	log.Println("OrderlyQueue Service started")

	// Start independent loops for Step 6 and Step 7
	go s.GithubEventLoop(ctx)
	go s.CICDEventLoop(ctx)

	for {
		// Step 1-2: Check Lock State
		locked, err := s.CheckLock(ctx)
		if err != nil {
			log.Printf("Error checking lock: %v", err)
			time.Sleep(s.cfg.Timeouts.LockSleep)
			continue
		}
		if locked {
			log.Println("Service is locked, waiting...")
			time.Sleep(s.cfg.Timeouts.LockSleep)
			continue
		}

		// Step 3: Fetch Next PR
		log.Println("Waiting for next PR...")
		prURL, err := s.FetchNextPR(ctx)
		if err != nil {
			if err == context.Canceled {
				return nil
			}
			log.Printf("Error fetching next PR: %v", err)
			time.Sleep(s.cfg.Timeouts.LockSleep)
			continue
		}
		log.Printf("Processing PR: %s", prURL)

		// Step 4-5: Dispatch Merge & Create Lock
		if err := s.DispatchMerge(ctx, prURL); err != nil {
			log.Printf("Error dispatching merge: %v", err)
			continue
		}
		// The lock creation with long expiry handles the "Waiting for merge" state
		if err := s.AcquireLock(ctx, prURL, s.cfg.Timeouts.LockExpiry); err != nil {
			log.Printf("Error acquiring lock: %v", err)
			continue
		}
		log.Printf("Merge dispatched and lock acquired for PR: %s", prURL)
	}
}

func (s *Service) GithubEventLoop(ctx context.Context) {
	pubsub := s.redis.Subscribe(ctx, s.cfg.Channels.GithubEvents)
	defer pubsub.Close()

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			var event models.GithubPREvent
			if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
				log.Printf("Error unmarshaling GitHub event: %v", err)
				continue
			}

			if event.PullRequest.State == "closed" && event.PullRequest.Merged {
				prURL := event.PullRequest.HTMLURL
				// Verify PR URL matches current lock key value
				currentPR, err := s.redis.Get(ctx, s.cfg.Keys.Lock).Result()
				if err != nil {
					if err != redis.Nil {
						log.Printf("Error getting current lock: %v", err)
					}
					continue
				}

				if currentPR == prURL {
					mergeSHA := event.PullRequest.MergeCommitSHA
					log.Printf("Merge completed for PR %s, SHA: %s", prURL, mergeSHA)
					// Store with a reasonably long expiry to avoid it sitting forever but long enough for CI/CD
					if err := s.redis.Set(ctx, s.cfg.Keys.MergeCommitSHA, mergeSHA, 1*time.Hour).Err(); err != nil {
						log.Printf("Error storing merge commit SHA: %v", err)
					}
				}
			}
		}
	}
}

func (s *Service) CICDEventLoop(ctx context.Context) {
	pubsub := s.redis.Subscribe(ctx, s.cfg.Channels.CICDEvents)
	defer pubsub.Close()

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			var event models.CICDCompletionEvent
			if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
				log.Printf("Error unmarshaling CICD event: %v", err)
				continue
			}

			if event.Event == "end" {
				// Correlation ID matching stored merge SHA
				storedSHA, err := s.redis.Get(ctx, s.cfg.Keys.MergeCommitSHA).Result()
				if err != nil {
					if err != redis.Nil {
						log.Printf("Error getting stored merge SHA: %v", err)
					}
					continue
				}

				if storedSHA == event.CorrelationID {
					log.Printf("CI/CD completed for SHA: %s", storedSHA)
					// Update lock key expiry to delay duration
					if err := s.UpdateLockExpiry(ctx, s.cfg.Timeouts.CICDDelay); err != nil {
						log.Printf("Error updating lock expiry: %v", err)
					} else {
						log.Printf("Lock expiry updated to delay duration: %v", s.cfg.Timeouts.CICDDelay)
					}
				}
			}
		}
	}
}

func main() {
	configPath := "config/config.yaml"
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		configPath = "config/config.example.yaml"
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
	}

	svc := NewService(cfg)
	ctx := context.Background()

	if err := svc.Run(ctx); err != nil {
		log.Fatalf("Service exited with error: %v", err)
	}
}
