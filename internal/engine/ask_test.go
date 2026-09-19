package engine

import (
	"strings"
	"testing"
	"time"
)

func TestAskTool_Success(t *testing.T) {
	a := &AskTool{Timeout: 5 * time.Second}
	done := make(chan struct{})
	var got string
	var askErr error
	go func() {
		defer close(done)
		got, askErr = a.Ask("Pick one?", []string{"a", "b"})
	}()
	// Wait until the question is registered, then answer it.
	var id string
	deadline := time.Now().Add(2 * time.Second)
	for {
		a.mu.Lock()
		for k := range a.Pending {
			id = k
		}
		a.mu.Unlock()
		if id != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("question was never registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.HasPrefix(id, "ask_") || len(id) != 4+12 {
		t.Fatalf("id = %q, want ask_<12hex>", id)
	}
	if !a.Answer(id, "a") {
		t.Fatal("Answer: returned false, want true")
	}
	<-done
	if askErr != nil {
		t.Fatalf("Ask: %v", askErr)
	}
	if got != "a" {
		t.Fatalf("got = %q, want a", got)
	}
}

func TestAskTool_DoubleAnswer(t *testing.T) {
	a := &AskTool{Timeout: 5 * time.Second}
	resCh := make(chan string, 1)
	go func() {
		got, err := a.Ask("Q?", nil)
		if err != nil {
			resCh <- "ERR:" + err.Error()
			return
		}
		resCh <- got
	}()
	var id string
	deadline := time.Now().Add(2 * time.Second)
	for id == "" {
		a.mu.Lock()
		for k := range a.Pending {
			id = k
		}
		a.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("question was never registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !a.Answer(id, "first") {
		t.Fatal("first Answer: false, want true")
	}
	if a.Answer(id, "second") {
		t.Fatal("second Answer: true, want false (only first wins)")
	}
	if got := <-resCh; got != "first" {
		t.Fatalf("Ask returned %q, want first", got)
	}
}

func TestAskTool_Timeout(t *testing.T) {
	a := &AskTool{Timeout: 50 * time.Millisecond}
	start := time.Now()
	_, err := a.Ask("Will this time out?", nil)
	if err == nil {
		t.Fatal("got nil error, want timeout")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "timed out") {
		t.Fatalf("err = %q, want timeout", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("Ask blocked far beyond the timeout")
	}
	a.mu.Lock()
	n := len(a.Pending)
	a.mu.Unlock()
	if n != 0 {
		t.Fatalf("pending = %d after timeout, want 0 (entry must be cleaned up)", n)
	}
}

func TestAskTool_Cancel(t *testing.T) {
	a := &AskTool{Timeout: 5 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		_, err := a.Ask("Q?", nil)
		errCh <- err
	}()
	var id string
	deadline := time.Now().Add(2 * time.Second)
	for id == "" {
		a.mu.Lock()
		for k := range a.Pending {
			id = k
		}
		a.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("question was never registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !a.Cancel(id) {
		t.Fatal("Cancel: false, want true")
	}
	if a.Cancel(id) {
		t.Fatal("second Cancel: true, want false")
	}
	if err := <-errCh; err == nil || !strings.Contains(strings.ToLower(err.Error()), "cancelled") {
		t.Fatalf("Ask err = %v, want cancellation", err)
	}
	if a.Answer("ask_deadbeefcafe", "x") {
		t.Fatal("Answer unknown id: true, want false")
	}
}

func TestAskTool_PrimaryOnly(t *testing.T) {
	nonPrimary := &AskTool{PrimaryOnly: true, IsPrimary: func() bool { return false }, Timeout: time.Second}
	if _, err := nonPrimary.Ask("Q?", nil); err == nil {
		t.Fatal("non-primary Ask: got nil error, want error")
	}
	primary := &AskTool{PrimaryOnly: true, IsPrimary: func() bool { return true }, Timeout: 50 * time.Millisecond}
	if _, err := primary.Ask("Q?", nil); err == nil {
		t.Fatal("primary Ask with short timeout: got nil error, want timeout (not a gate error)")
	} else if strings.Contains(err.Error(), "primary") {
		t.Fatalf("primary Ask: gate error %q, want timeout", err)
	}
}
