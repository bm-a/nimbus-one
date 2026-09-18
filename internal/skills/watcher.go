package skills

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// DefaultWatchInterval is the fallback poll period for Watcher.
const DefaultWatchInterval = 10 * time.Second

// Watcher monitors SKILL.md files and invokes OnChange on add/remove/modify.
// It prefers fsnotify event-driven reload and falls back to polling when the
// platform cannot watch (some Termux kernels, network mounts, Windows quirks).
// Either way the agent never needs a restart to pick up new skills.
type Watcher struct {
	Dir      string
	OnChange func()
	// Interval overrides the poll period; <=0 means DefaultWatchInterval.
	// Interval also debounces fsnotify bursts.
	Interval time.Duration
}

// Start blocks until ctx is done.
func (w *Watcher) Start(ctx context.Context) error {
	if err := w.watchEvents(ctx); err != nil {
		// fsnotify unavailable (or root missing) — degrade to polling.
		return w.poll(ctx)
	}
	return nil
}

// watchEvents uses fsnotify with debounce; errors when unwatched.
func (w *Watcher) watchEvents(ctx context.Context) error {
	interval := w.Interval
	if interval <= 0 {
		interval = DefaultWatchInterval
	}
	debounce := 500 * time.Millisecond
	if interval < debounce {
		debounce = interval
	}
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer fw.Close()
	// Watch root + one level of subdirs (flat + category/name layouts).
	dirs := map[string]bool{w.Dir: true}
	for _, p := range discoverSkillFiles(w.Dir) {
		dirs[filepath.Dir(p)] = true
	}
	added := 0
	for d := range dirs {
		if err := fw.Add(d); err == nil {
			added++
		}
	}
	if added == 0 {
		return errors.New("fsnotify: no watchable directories, falling back to polling")
	}
	var timer *time.Timer
	fire := func() {
		if w.OnChange != nil {
			w.OnChange()
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-fw.Events:
			if !ok {
				return w.poll(ctx)
			}
			if !isSkillEvent(ev.Name) {
				continue
			}
			// New subdir appearing mid-run: watch it too (best effort).
			if ev.Op&(fsnotify.Create) != 0 {
				_ = fw.Add(ev.Name)
			}
			if timer == nil {
				timer = time.AfterFunc(debounce, fire)
			} else {
				timer.Reset(debounce)
			}
		case err, ok := <-fw.Errors:
			if !ok {
				return w.poll(ctx)
			}
			_ = err // log-free library: keep watching; poll loop catches drift
		}
	}
}

// poll is the portable fallback: fingerprint SKILL.md files each interval.
func (w *Watcher) poll(ctx context.Context) error {
	interval := w.Interval
	if interval <= 0 {
		interval = DefaultWatchInterval
	}
	prev := fingerprintSkills(w.Dir)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			cur := fingerprintSkills(w.Dir)
			if !fingerprintsEqual(prev, cur) {
				prev = cur
				if w.OnChange != nil {
					w.OnChange()
				}
			}
		}
	}
}

func isSkillEvent(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return base == "skill.md"
}

// fingerprintSkills maps SKILL.md path -> "mtime:size" (polling fallback).
func fingerprintSkills(root string) map[string]string {
	out := map[string]string{}
	for _, p := range discoverSkillFiles(root) {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		out[p] = fmt.Sprintf("%d:%d", st.ModTime().UnixNano(), st.Size())
	}
	return out
}

func fingerprintsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
