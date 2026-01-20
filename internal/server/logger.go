package server

import (
	"fmt"
	"os"
	"strconv"
	"sync"

	"persishtent/internal/config"
	"persishtent/internal/session"
)

// LogRotator handles writing to a log file with size-based rotation.
type LogRotator struct {
	name        string
	basePath    string
	currentFile *os.File
	size        int64
	maxSize     int64
	maxFiles    int
	mu          sync.Mutex
}

// NewLogRotator creates a new LogRotator.
func NewLogRotator(name string, path string) (*LogRotator, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}

	maxSize := int64(config.Global.LogRotationSizeMB) * 1024 * 1024
	if maxSize <= 0 {
		maxSize = 1024 * 1024 // Fallback to 1MB
	}

	return &LogRotator{
		name:        name,
		basePath:    path,
		currentFile: f,
		maxSize:     maxSize,
		maxFiles:    config.Global.MaxLogRotations,
	}, nil
}

// Write implements io.Writer. It writes data to the log file, rotating if necessary.
func (l *LogRotator) Write(p []byte) (n int, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.size+int64(len(p)) > l.maxSize {
		if err := l.rotate(); err != nil {
			// If rotation fails, log to stderr but continue writing to current file
			// to avoid data loss.
			fmt.Fprintf(os.Stderr, "Log rotation failed: %v\n", err)
		}
	}

	n, err = l.currentFile.Write(p)
	if err == nil {
		l.size += int64(n)
	}
	return n, err
}

// Close closes the underlying file.
func (l *LogRotator) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.currentFile.Close()
}

// rotate performs the log rotation.
func (l *LogRotator) rotate() error {
	_ = l.currentFile.Close()

	// Find highest index
	files, err := session.GetLogFiles(l.name)
	if err != nil {
		// Try to reopen if getting files fails
		_ = l.reopen()
		return err
	}

	maxIdx := 0
	prefix := l.basePath + "."
	for _, f := range files {
		// session.GetLogFiles returns full paths
		if len(f) > len(prefix) && f[:len(prefix)] == prefix {
			idx, err := strconv.Atoi(f[len(prefix):])
			if err == nil && idx > maxIdx {
				maxIdx = idx
			}
		}
	}

	nextIdx := maxIdx + 1
	newName := fmt.Sprintf("%s.%d", l.basePath, nextIdx)
	if err := os.Rename(l.basePath, newName); err != nil {
		_ = l.reopen()
		return err
	}

	// Cleanup old rotations if limit exceeded
	// Get files again or use our list (files was sorted oldest to newest by session.GetLogFiles)
	// But wait, session.GetLogFiles includes active log at the end usually.
	// Let's rely on the list we got *before* rename.
	// files[0] is oldest rotated log.
	
	// We just created a NEW rotated file (nextIdx).
	// So we have 1 more file than `files` list implies?
	// No, `files` included the active log (l.basePath).
	// After rename, l.basePath is gone (it's now newName).
	// So total count of *rotated* files is now (old_rotated + 1).
	// If total count > maxFiles, remove oldest.
	
	// Let's simplify: call GetLogFiles again? No, race condition?
	// The `files` list contains all logs including active one.
	// If `len(files) >= l.maxFiles`, we need to remove the oldest.
	// Note: `maxFiles` usually means "keep N rotated logs" or "N total logs"?
	// Config says `MaxLogRotations`. Usually implies N history files + 1 active.
	// `session.go` check was: `if len(files) >= session.MaxLogRotations { remove(files[0]) }`
	// `files` included active log. So `MaxLogRotations` acts as "Total Log Files Retention".
	
	if len(files) >= l.maxFiles {
		// files[0] is the oldest
		// Ensure we don't delete what we just renamed if maxFiles is 1?
		// files[0] is likely `log.1` or `log.N`.
		// active log is usually last in `files`.
		toRemove := files[0]
		// Sanity check: don't remove current active log path (though it should be renamed by now)
		if toRemove != l.basePath {
			_ = os.Remove(toRemove)
		}
	}

	return l.reopen()
}

func (l *LogRotator) reopen() error {
	f, err := os.OpenFile(l.basePath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0600)
	if err != nil {
		// Fatal: can't open log file.
		// Try append mode as fallback?
		f, err = os.OpenFile(l.basePath, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0600)
	}
	
	if err == nil {
		l.currentFile = f
		l.size = 0
	}
	return err
}