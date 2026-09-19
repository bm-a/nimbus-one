// Config-file watcher primitive for gateway reloads.
//
// OpenClaw reference (read-only mirror /data/data/com.termux/files/home/tmp/openclaw-src):
//
//	src/gateway/server-reload-*.ts — hot/restart reload contracts, active-call
//	                                 draining, channel restart sequencing
//	src/gateway/server-startup-*    — GatewayReloadMode off|restart|hot|hybrid
//	                                 selection at boot
//
// This file provides only the stdlib polling primitive: Watcher polls a
// path's mtime/size and invokes OnChange on appearance, disappearance, or
// modification. Polling (not fsnotify) keeps the gateway stdlib-only by
// convention. Signal wiring (SIGHUP) and the off|restart|hot|hybrid mode
// switch live in main.go, which consumes this primitive.
package gateway

import (
	"context"
	"os"
	"sync"
	"time"
)

// defaultWatchInterval applies when Watcher.Interval is non-positive.
const defaultWatchInterval = 5 * time.Second

// Watcher polls Path and calls OnChange whenever the file appears,
// disappears, or changes size/mtime. The zero value is usable after setting
// Path; an OnChange of nil is allowed (changes are still reported by Poll).
type Watcher struct {
	Path     string
	OnChange func()
	Interval time.Duration

	mu       sync.Mutex
	have     bool
	lastMod  time.Time
	lastSize int64
	lastGone bool
}

// NewWatcher builds a Watcher for path; a non-positive interval means 5s.
func NewWatcher(path string, interval time.Duration, onChange func()) *Watcher {
	return &Watcher{Path: path, Interval: interval, OnChange: onChange}
}

func (w *Watcher) every() time.Duration {
	if w == nil || w.Interval <= 0 {
		return defaultWatchInterval
	}
	return w.Interval
}

// Poll performs a single check and reports whether a change fired. The first
// call only establishes the baseline and returns false. When a change fires,
// OnChange runs synchronously with panics recovered so a bad callback cannot
// kill the watch loop.
func (w *Watcher) Poll() bool {
	if w == nil {
		return false
	}
	fi, err := os.Stat(w.Path)
	gone := err != nil
	var mod time.Time
	var size int64
	if !gone {
		mod = fi.ModTime()
		size = fi.Size()
	}
	w.mu.Lock()
	if !w.have {
		w.have, w.lastMod, w.lastSize, w.lastGone = true, mod, size, gone
		w.mu.Unlock()
		return false
	}
	fired := gone != w.lastGone || (!gone && (mod != w.lastMod || size != w.lastSize))
	if fired {
		w.lastMod, w.lastSize, w.lastGone = mod, size, gone
	}
	cb := w.OnChange
	w.mu.Unlock()
	if fired && cb != nil {
		func() {
			defer func() { _ = recover() }()
			cb()
		}()
	}
	return fired
}

// Run polls every interval until ctx ends.
func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(w.every())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.Poll()
		}
	}
}
