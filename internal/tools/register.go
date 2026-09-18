package tools

import (
	"path/filepath"
	"time"
)

// RegisterBuiltins registers the stock toolset with sensible defaults,
// scoping filesystem tools (and the bash cwd default) to workdir.
func RegisterBuiltins(r *Registry, workdir string) {
	if r == nil {
		return
	}
	if workdir == "" {
		workdir = "."
	}
	abs, err := filepath.Abs(workdir)
	if err != nil {
		abs = workdir
	}
	allow := []string{abs}
	r.Register(&BashTool{Timeout: 60 * time.Second, AllowDirs: allow})
	r.Register(&ReadTool{AllowDirs: allow})
	r.Register(&WriteTool{AllowDirs: allow})
	r.Register(&ListTool{AllowDirs: allow})
	r.Register(&SearchTool{AllowDirs: allow})
	r.Register(&FetchTool{Timeout: 30 * time.Second})
	r.Register(&SearchTool2{Timeout: 30 * time.Second})
	r.Register(&OpencodeTool{Dir: abs})
	r.Register(&TranscribeTool{})
	r.Register(&SpeakTool{})
}
