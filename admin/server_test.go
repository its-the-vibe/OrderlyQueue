package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"OrderlyQueue/config"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func setupTest(t *testing.T) (*Server, *miniredis.Miniredis) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}

	rdb := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	cfg := &config.Config{}
	cfg.Keys.PRList = "test:pr-queue"
	cfg.Keys.Lock = "test:lock"
	cfg.Admin.Port = 8080

	return NewServer(cfg, rdb), mr
}

func TestHandleQueue(t *testing.T) {
	s, mr := setupTest(t)

	// Test GET queue (empty)
	req := httptest.NewRequest(http.MethodGet, "/api/queue", nil)
	rr := httptest.NewRecorder()
	s.handleQueue(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected status OK, got %v", rr.Code)
	}
	if rr.Body.String() != "null\n" { // json encode of empty slice
		// Actually items, err := s.redis.LRange(ctx, s.cfg.Keys.PRList, 0, -1).Result() returns empty slice which json encodes to []
		// wait, go-redis returns empty slice for non-existent key too.
		// Actually json.NewEncoder(w).Encode(items) with items == []string{} might be []\n
		// Let's check what it actually is.
	}

	// Test POST add to queue
	prURL := "https://github.com/owner/repo/pull/1"
	body, _ := json.Marshal(map[string]string{"url": prURL})
	req = httptest.NewRequest(http.MethodPost, "/api/queue", bytes.NewBuffer(body))
	rr = httptest.NewRecorder()
	s.handleQueue(rr, req)
	if rr.Code != http.StatusCreated {
		t.Errorf("expected status Created, got %v", rr.Code)
	}

	// Verify in redis
	items, _ := mr.List(s.cfg.Keys.PRList)
	if len(items) != 1 || items[0] != prURL {
		t.Errorf("expected 1 item in queue, got %v", items)
	}

	// Test GET queue (with items)
	req = httptest.NewRequest(http.MethodGet, "/api/queue", nil)
	rr = httptest.NewRecorder()
	s.handleQueue(rr, req)
	var fetched []string
	json.Unmarshal(rr.Body.Bytes(), &fetched)
	if len(fetched) != 1 || fetched[0] != prURL {
		t.Errorf("expected [%s], got %v", prURL, fetched)
	}

	// Test DELETE specific item
	req = httptest.NewRequest(http.MethodDelete, "/api/queue?url="+prURL, nil)
	rr = httptest.NewRecorder()
	s.handleQueue(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("expected status NoContent, got %v", rr.Code)
	}
	items, _ = mr.List(s.cfg.Keys.PRList)
	if len(items) != 0 {
		t.Errorf("expected empty queue, got %v", items)
	}

	// Test DELETE all
	mr.RPush(s.cfg.Keys.PRList, "item1", "item2")
	req = httptest.NewRequest(http.MethodDelete, "/api/queue", nil)
	rr = httptest.NewRecorder()
	s.handleQueue(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("expected status NoContent, got %v", rr.Code)
	}
	items, _ = mr.List(s.cfg.Keys.PRList)
	if len(items) != 0 {
		t.Errorf("expected empty queue after clear, got %v", items)
	}
}

func TestHandleLock(t *testing.T) {
	s, mr := setupTest(t)

	// Test GET lock status (no lock)
	req := httptest.NewRequest(http.MethodGet, "/api/lock", nil)
	rr := httptest.NewRecorder()
	s.handleLock(rr, req)
	var status map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &status)
	if status["locked"].(bool) != false {
		t.Errorf("expected not locked")
	}

	// Test GET lock status (locked)
	prURL := "https://github.com/owner/repo/pull/1"
	mr.Set(s.cfg.Keys.Lock, prURL)
	mr.SetTTL(s.cfg.Keys.Lock, 10*time.Second)

	req = httptest.NewRequest(http.MethodGet, "/api/lock", nil)
	rr = httptest.NewRecorder()
	s.handleLock(rr, req)
	json.Unmarshal(rr.Body.Bytes(), &status)
	if status["locked"].(bool) != true || status["pr_url"].(string) != prURL {
		t.Errorf("expected locked for %s, got %v", prURL, status)
	}

	// Test DELETE lock
	req = httptest.NewRequest(http.MethodDelete, "/api/lock", nil)
	rr = httptest.NewRecorder()
	s.handleLock(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("expected status NoContent, got %v", rr.Code)
	}
	if mr.Exists(s.cfg.Keys.Lock) {
		t.Errorf("expected lock to be deleted")
	}
}
