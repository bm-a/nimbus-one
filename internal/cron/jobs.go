// Cron job registry (Phase 3): named schedules with briefs.
//
// In-memory in V1: a daemon restart drops jobs (documented). SQLite
// persistence is the follow-up once daemon-driven execution lands.
// Due() is pure and testable; execution belongs to the caller.
package cron

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Job is one scheduled brief.
type Job struct {
	ID      string
	Spec    string
	Brief   string
	Created time.Time
	LastRun time.Time
	sched   Schedule
}

// Registry holds jobs.
type Registry struct {
	mu  sync.Mutex
	seq int64
	m   map[string]*Job
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry { return &Registry{m: map[string]*Job{}} }

// Add parses spec and registers the brief. Returns the job id.
func (r *Registry) Add(spec, brief string) (string, error) {
	if strings.TrimSpace(brief) == "" {
		return "", fmt.Errorf("cron: empty brief")
	}
	sched, err := Parse(spec)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	id := fmt.Sprintf("cron-%d", r.seq)
	r.m[id] = &Job{ID: id, Spec: sched.Spec(), Brief: brief, Created: time.Now(), sched: sched}
	return id, nil
}

// Remove deletes a job. False when unknown.
func (r *Registry) Remove(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.m[id]; !ok {
		return false
	}
	delete(r.m, id)
	return true
}

// List returns copies newest-first.
func (r *Registry) List() []Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Job, 0, len(r.m))
	for _, j := range r.m {
		cp := *j
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// Due returns jobs whose schedule fires between their LastRun and now.
func (r *Registry) Due(now time.Time) []Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Job
	for _, j := range r.m {
		if j.sched.Due(j.LastRun, now) {
			cp := *j
			out = append(out, cp)
		}
	}
	return out
}

// MarkRan records an execution timestamp.
func (r *Registry) MarkRan(id string, t time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if j, ok := r.m[id]; ok {
		j.LastRun = t
	}
}
