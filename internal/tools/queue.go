// Mutation queue: per-path serialization for file mutations.
//
// OpenClaw reference (read-only):
//
//	sessions/tools/edit.ts + write.ts (ordered application, one writer per
//	path at a time). Nimbus port keeps the guarantee with stdlib sync only.
package tools

import "sync"

// MutationQueue serializes mutating operations that target the same path.
// Operations on different paths run concurrently.
type MutationQueue struct {
	mu    sync.Mutex
	locks map[string]*pathLock
}

type pathLock struct {
	mu   sync.Mutex
	refs int
}

// NewMutationQueue creates an empty queue.
func NewMutationQueue() *MutationQueue {
	return &MutationQueue{locks: map[string]*pathLock{}}
}

// Run executes fn while holding the mutex for path, so concurrent callers
// mutating the same file are serialized in arrival order.
func (q *MutationQueue) Run(path string, fn func() (string, error)) (string, error) {
	l := q.acquire(path)
	defer q.release(path)
	l.mu.Lock()
	defer l.mu.Unlock()
	return fn()
}

func (q *MutationQueue) acquire(path string) *pathLock {
	q.mu.Lock()
	defer q.mu.Unlock()
	l, ok := q.locks[path]
	if !ok {
		l = &pathLock{}
		q.locks[path] = l
	}
	l.refs++
	return l
}

func (q *MutationQueue) release(path string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	l, ok := q.locks[path]
	if !ok {
		return
	}
	l.refs--
	if l.refs <= 0 {
		delete(q.locks, path)
	}
}

// Len reports the number of tracked paths (for tests/debugging).
func (q *MutationQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.locks)
}

// sharedMutations serializes same-file edits/writes across tool instances.
var sharedMutations = NewMutationQueue()
