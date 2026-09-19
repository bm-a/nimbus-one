package tools

import (
	"path/filepath"
	"time"
)

// RegisterBuiltins registers the stock toolset with sensible defaults,
// scoping filesystem tools (and the bash cwd default) to workdir.
//
// Toolset provenance (OpenClaw read-only refs): edit ← sessions/tools/edit.ts,
// process ← bash-tools/exec-runtime.ts + exec-defaults.ts (background +
// supervisor), read_paged ← sessions/tools/read.ts (cursors) + ls/find/grep
// (gitignore), web_search chain ← web-fetch/runtime.ts + web-search/runtime.ts
// (provider mesh), computer ← extensions/browser + computer-tool-node.ts.
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
	r.Register(&EditTool{AllowDirs: allow})
	r.Register(&ProcessTool{AllowDirs: allow, Manager: NewProcessManager()})
	r.Register(&PagedReadTool{AllowDirs: allow})
	r.Register(&ComputerTool{})
	r.Register(&OpencodeTool{Dir: abs})
	r.Register(&TranscribeTool{})
	r.Register(&SpeakTool{})
}
