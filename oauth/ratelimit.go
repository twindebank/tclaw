package oauth

import (
	"net/http"
	"sync"
	"time"
)

const (
	// Max requests per IP within the rate limit window.
	rateLimitRequests = 20
	// Window over which requests are counted.
	rateLimitWindow = 1 * time.Minute
	// How often to purge expired entries from the rate limiter.
	rateLimitGCInterval = 5 * time.Minute
	// Maximum request body size. Telegram updates are typically <10 KiB;
	// OAuth callbacks have no body. 1 MiB is generous but prevents memory exhaustion.
	maxRequestBodyBytes = 1 << 20 // 1 MiB
)

type rateLimitEntry struct {
	count       int
	windowStart time.Time
}

// RateLimiter is a simple per-IP fixed-window rate limiter.
type RateLimiter struct {
	mu      sync.Mutex
	entries map[string]*rateLimitEntry
	stopGC  chan struct{}
}

// NewRateLimiter creates a rate limiter and starts its cleanup goroutine.
func NewRateLimiter() *RateLimiter {
	rl := &RateLimiter{
		entries: make(map[string]*rateLimitEntry),
		stopGC:  make(chan struct{}),
	}
	go rl.gc()
	return rl
}

// Stop shuts down the cleanup goroutine.
func (rl *RateLimiter) Stop() {
	close(rl.stopGC)
}

// Allow returns true if the request from this IP should be allowed.
func (rl *RateLimiter) Allow(ip string) bool {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, ok := rl.entries[ip]
	if !ok || now.Sub(entry.windowStart) > rateLimitWindow {
		// New window.
		rl.entries[ip] = &rateLimitEntry{count: 1, windowStart: now}
		return true
	}

	entry.count++
	return entry.count <= rateLimitRequests
}

// gc periodically removes expired entries to prevent unbounded memory growth.
func (rl *RateLimiter) gc() {
	ticker := time.NewTicker(rateLimitGCInterval)
	defer ticker.Stop()
	for {
		select {
		case <-rl.stopGC:
			return
		case <-ticker.C:
			now := time.Now()
			rl.mu.Lock()
			for ip, entry := range rl.entries {
				if now.Sub(entry.windowStart) > rateLimitWindow {
					delete(rl.entries, ip)
				}
			}
			rl.mu.Unlock()
		}
	}
}

// Middleware wraps an http.Handler with per-IP rate limiting.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		// Only trust Fly-Client-IP which is set by Fly.io's proxy and cannot
		// be spoofed by clients. X-Forwarded-For is client-controlled and
		// trivially faked to bypass rate limits.
		if flyIP := r.Header.Get("Fly-Client-IP"); flyIP != "" {
			ip = flyIP
		}

		if !rl.Allow(ip) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		// Cap request body size to prevent memory exhaustion from oversized payloads.
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)

		next.ServeHTTP(w, r)
	})
}
