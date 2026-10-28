package store

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestSetGet(t *testing.T) {
	s := New()
	if _, ok := s.Get("c1"); ok {
		t.Fatal("unexpected hit on empty store")
	}

	raw := []byte(`{"a":1}`)
	s.Set(Status{
		Name:     "c1",
		Time:     time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC),
		SourceIP: "1.2.3.4",
		Status:   raw,
	})
	got, ok := s.Get("c1")
	if !ok {
		t.Fatal("miss after set")
	}
	if !bytes.Equal(got.Status, raw) {
		t.Errorf("status = %s, want %s", got.Status, raw)
	}
	if got.SourceIP != "1.2.3.4" {
		t.Errorf("SourceIP = %q", got.SourceIP)
	}
	if !got.Time.Equal(time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Errorf("Time = %v", got.Time)
	}

	// last status wins
	s.Set(Status{Name: "c1", Time: time.Now(), SourceIP: "5.6.7.8", Status: []byte(`2`)})
	got, _ = s.Get("c1")
	if got.SourceIP != "5.6.7.8" || !bytes.Equal(got.Status, []byte(`2`)) {
		t.Errorf("overwrite failed: %+v", got)
	}
}

func TestList(t *testing.T) {
	s := New()
	s.Set(Status{Name: "zeta", Time: time.Now(), SourceIP: "1.1.1.1", Status: []byte(`{}`)})

	infos := s.List([]string{"beta", "zeta", "alpha"})
	if len(infos) != 3 {
		t.Fatalf("List len = %d", len(infos))
	}
	if infos[0].Name != "alpha" || infos[0].LastTime != nil {
		t.Errorf("infos[0] = %+v", infos[0])
	}
	if infos[1].Name != "beta" || infos[1].LastTime != nil {
		t.Errorf("infos[1] = %+v", infos[1])
	}
	if infos[2].Name != "zeta" || infos[2].LastTime == nil {
		t.Errorf("infos[2] = %+v", infos[2])
	}
}

func TestConcurrent(t *testing.T) {
	s := New()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("c%d", i%4)
			for j := 0; j < 100; j++ {
				s.Set(Status{Name: name, Time: time.Now(), SourceIP: "1.1.1.1", Status: []byte(`{}`)})
				s.Get(name)
			}
		}(i)
	}
	wg.Wait()
	if _, ok := s.Get("c3"); !ok {
		t.Fatal("c3 should exist after concurrent writes")
	}
}
