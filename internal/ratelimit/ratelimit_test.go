package ratelimit

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestAllow(t *testing.T) {
	l := New()
	now := time.Now()
	l.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if !l.Allow("a", 3, time.Minute) {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
	if l.Allow("a", 3, time.Minute) {
		t.Fatal("4th request should be denied")
	}
	if !l.Allow("b", 3, time.Minute) {
		t.Fatal("a different key should be unaffected")
	}

	now = now.Add(time.Minute)
	if !l.Allow("a", 3, time.Minute) {
		t.Fatal("request after window reset should be allowed")
	}
}

func TestNoLimit(t *testing.T) {
	l := New()
	for i := 0; i < 10; i++ {
		if !l.Allow("a", 0, time.Minute) {
			t.Fatalf("request %d should be allowed with no limit", i+1)
		}
	}
	if !l.Allow("a", 5, 0) {
		t.Fatal("zero window should mean no limit")
	}
}

func TestSweep(t *testing.T) {
	l := New()
	now := time.Now()
	l.now = func() time.Time { return now }

	l.Allow("old", 5, time.Minute)
	l.Allow("new", 5, time.Minute)

	now = now.Add(3 * time.Minute)
	l.Sweep(2 * time.Minute)

	if l.Len() != 0 {
		t.Fatalf("Sweep left %d entries", l.Len())
	}

	// entries within maxAge survive
	l.Allow("fresh", 5, time.Minute)
	l.Sweep(2 * time.Minute)
	if l.Len() != 1 {
		t.Fatalf("Sweep removed a fresh entry, len=%d", l.Len())
	}
}

func TestStartSweeper(t *testing.T) {
	var nowPtr atomic.Pointer[time.Time]
	t0 := time.Now()
	nowPtr.Store(&t0)
	l := New()
	l.now = func() time.Time { return *nowPtr.Load() }

	stop := make(chan struct{})
	defer close(stop)
	go l.StartSweeper(5*time.Millisecond, func() time.Duration { return time.Second }, stop)

	l.Allow("a", 5, time.Second)
	later := t0.Add(5 * time.Second) // bucket now older than the window
	nowPtr.Store(&later)

	deadline := time.Now().Add(2 * time.Second)
	for l.Len() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if l.Len() != 0 {
		t.Fatalf("sweeper left %d entries", l.Len())
	}
}
