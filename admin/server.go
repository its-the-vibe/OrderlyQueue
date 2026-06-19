package admin

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"time"

	"OrderlyQueue/config"

	"github.com/redis/go-redis/v9"
)

//go:embed static/*
var staticFiles embed.FS

type Server struct {
	cfg   *config.Config
	redis *redis.Client
}

func NewServer(cfg *config.Config, rdb *redis.Client) *Server {
	return &Server{
		cfg:   cfg,
		redis: rdb,
	}
}

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// API Endpoints
	mux.HandleFunc("/api/queue", s.handleQueue)
	mux.HandleFunc("/api/lock", s.handleLock)

	// Static Files
	staticFS, _ := fs.Sub(staticFiles, "static")
	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", s.cfg.Admin.Port),
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	log.Printf("Admin UI starting on port %d", s.cfg.Admin.Port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) handleQueue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	switch r.Method {
	case http.MethodGet:
		s.getQueue(ctx, w)
	case http.MethodPost:
		s.addToQueue(ctx, w, r)
	case http.MethodDelete:
		s.removeFromQueue(ctx, w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) getQueue(ctx context.Context, w http.ResponseWriter) {
	items, err := s.redis.LRange(ctx, s.cfg.Keys.PRList, 0, -1).Result()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

func (s *Server) addToQueue(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if body.URL == "" {
		http.Error(w, "URL is required", http.StatusBadRequest)
		return
	}

	if err := s.redis.RPush(ctx, s.cfg.Keys.PRList, body.URL).Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("[ADMIN] Added PR to queue: %s", body.URL)
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) removeFromQueue(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	url := r.URL.Query().Get("url")
	if url != "" {
		// Remove specific item
		if err := s.redis.LRem(ctx, s.cfg.Keys.PRList, 0, url).Err(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("[ADMIN] Removed PR from queue: %s", url)
	} else {
		// Clear all
		if err := s.redis.Del(ctx, s.cfg.Keys.PRList).Err(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("[ADMIN] Cleared queue")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLock(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	switch r.Method {
	case http.MethodGet:
		s.getLockStatus(ctx, w)
	case http.MethodDelete:
		s.releaseLock(ctx, w)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) getLockStatus(ctx context.Context, w http.ResponseWriter) {
	val, err := s.redis.Get(ctx, s.cfg.Keys.Lock).Result()
	if err == redis.Nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"locked": false,
		})
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ttl, err := s.redis.TTL(ctx, s.cfg.Keys.Lock).Result()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"locked": true,
		"pr_url": val,
		"ttl_seconds": ttl.Seconds(),
	})
}

func (s *Server) releaseLock(ctx context.Context, w http.ResponseWriter) {
	if err := s.redis.Del(ctx, s.cfg.Keys.Lock).Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("[ADMIN] Forcefully released lock")
	w.WriteHeader(http.StatusNoContent)
}
