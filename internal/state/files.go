// Package state provides flat-file workspace handling.
package state

import (
	"os"
	"path/filepath"
	"time"
)

// File names managed by Workspace.
const (
	SoulFile      = "SOUL.md"
	UserFile      = "USER.md"
	MemoryFile    = "MEMORY.md"
	HeartbeatFile = "HEARTBEAT.md"
)

// Aliases for convenience.
const (
	SoulName      = SoulFile
	UserName      = UserFile
	MemoryName    = MemoryFile
	HeartbeatName = HeartbeatFile
)

// Workspace is a flat-file directory holding markdown state files.
type Workspace struct {
	Dir string
}

// Load reads Dir/name and returns its content.
// It returns "" with no error if the file does not exist.
func (w Workspace) Load(name string) (string, error) {
	p := filepath.Join(w.Dir, name)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// Save writes content to Dir/name atomically (write tmp + rename) with 0644.
func (w Workspace) Save(name, content string) error {
	if err := os.MkdirAll(w.Dir, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(w.Dir, name)
	tmp, err := os.CreateTemp(w.Dir, name+".tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Best effort cleanup on failure.
	success := false
	defer func() {
		_ = tmp.Close()
		if !success {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.WriteString(content); err != nil {
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return err
	}
	success = true
	// Ensure final mode even if umask interfered.
	_ = os.Chmod(dst, 0o644)
	return nil
}

// Append appends entry to Dir/name with a timestamp header:
//
//	"\n\n## <RFC3339>\n" + entry
//
// If the file does not exist or is empty, the leading blank line is omitted.
func (w Workspace) Append(name, entry string) error {
	if err := os.MkdirAll(w.Dir, 0o755); err != nil {
		return err
	}
	p := filepath.Join(w.Dir, name)
	var prefix string
	ts := time.Now().UTC().Format(time.RFC3339)
	if st, err := os.Stat(p); err == nil && st.Size() > 0 {
		prefix = "\n\n## " + ts + "\n"
	} else if os.IsNotExist(err) {
		prefix = "## " + ts + "\n"
	} else if err != nil {
		return err
	} else {
		// Exists but empty.
		prefix = "## " + ts + "\n"
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(prefix + entry); err != nil {
		return err
	}
	if len(entry) == 0 || entry[len(entry)-1] != '\n' {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}
	return nil
}

// EnsureDefaults writes soul/user/memory/heartbeat contents only if missing.
func (w Workspace) EnsureDefaults(soul, user, memory, heartbeat string) error {
	if err := os.MkdirAll(w.Dir, 0o755); err != nil {
		return err
	}
	pairs := [][2]string{
		{SoulFile, soul},
		{UserFile, user},
		{MemoryFile, memory},
		{HeartbeatFile, heartbeat},
	}
	for _, pr := range pairs {
		p := filepath.Join(w.Dir, pr[0])
		if _, err := os.Stat(p); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := w.Save(pr[0], pr[1]); err != nil {
			return err
		}
	}
	return nil
}

// LoadSoul loads SOUL.md.
func (w Workspace) LoadSoul() (string, error) { return w.Load(SoulFile) }

// LoadUser loads USER.md.
func (w Workspace) LoadUser() (string, error) { return w.Load(UserFile) }

// LoadMemory loads MEMORY.md.
func (w Workspace) LoadMemory() (string, error) { return w.Load(MemoryFile) }

// LoadHeartbeat loads HEARTBEAT.md.
func (w Workspace) LoadHeartbeat() (string, error) { return w.Load(HeartbeatFile) }
