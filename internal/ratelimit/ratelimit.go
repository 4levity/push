// Package ratelimit implements a basic per-key fixed-window counter.
package ratelimit

import (
	"sync"
	"time"
)

type bucket struct {
	start time.Time
	count int
}

// Limiter allows at most MaxRequests requests per key per Window.
type Limiter struct {
	mu  sync.Mutex
	now func() time.Time
	ips map[string]*bucket
}

// New returns an empty Limiter.
func New() *Limiter {
	return &Limiter{now: time.Now, ips: make(map[string]*bucket)}
}

// Allow reports whether a request from key is allowed under the given limit,
// counting it if so. maxRequests <= 0 or window <= 0 means "no limit".
func (l *Limiter) Allow(key string, maxRequests int, window time.Duration) bool {
	if maxRequests <= 0 || window <= 0 {
		return true
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.ips[key]
	if !ok || now.Sub(b.start) >= window {
		l.ips[key] = &bucket{start: now, count: 1}
		return true
	}
	b.count++
	return b.count <= maxRequests
}

// Len returns the number of tracked keys (mainly useful for tests).
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.ips)
}

// Sweep drops entries whose window started more than maxAge ago.
func (l *Limiter) Sweep(maxAge time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, b := range l.ips {
		if now.Sub(b.start) > maxAge {
			delete(l.ips, k)
		}
	}
}

// StartSweeper runs Sweep every interval until stop is closed.
func (l *Limiter) StartSweeper(interval, maxAge time.Duration, stop <-chan struct{}) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			l.Sweep(maxAge)
		case <-stop:
			return
		}
	}
}
