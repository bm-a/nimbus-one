package session

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestLanes_Serializes(t *testing.T) {
	l := NewLanes()
	rel1, err := l.Acquire(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !l.Held("s1") {
		t.Fatal("lane must report held")
	}
	if l.Held("other") {
		t.Fatal("other lane must be free")
	}
	// Second acquire blocks until release.
	done := make(chan struct{})
	go func() {
		defer close(done)
		rel2, err := l.Acquire(context.Background(), "s1")
		if err != nil {
			t.Errorf("acquire: %v", err)
			return
		}
		rel2()
	}()
	select {
	case <-done:
		t.Fatal("second acquire must block while held")
	case <-time.After(50 * time.Millisecond):
	}
	rel1()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("second acquire must proceed after release")
	}
}

func TestLanes_CtxCancel(t *testing.T) {
	l := NewLanes()
	rel, err := l.Acquire(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer rel()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := l.Acquire(ctx, "s1"); err == nil {
		t.Fatal("cancelled acquire must error")
	}
}

func TestLanes_ConcurrentCounter(t *testing.T) {
	l := NewLanes()
	var mu sync.Mutex
	n := 0
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel, err := l.Acquire(context.Background(), "shared")
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			defer rel()
			mu.Lock()
			n++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if n != 20 {
		t.Fatalf("n = %d, want 20", n)
	}
}
