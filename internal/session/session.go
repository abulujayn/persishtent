package session

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"syscall"
	"time"
)

var nameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ValidateName checks if a session name is valid
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("session name cannot be empty")
	}
	if !nameRegex.MatchString(name) {
		return fmt.Errorf("session name must only contain alphanumeric characters, underscores, and hyphens")
	}
	return nil
}

const (
	DirName          = ".persishtent"
	MaxLogRotations = 5
)

// Info holds information about a persistent session
type Info struct {
	Name      string    `json:"name"`
	PID       int       `json:"pid"`
	Command   string    `json:"command"`
	LogPath   string    `json:"log_path"`
	StartTime time.Time `json:"start_time"`
}

// GetSSHSockPath returns the path to the stable ssh-agent symlink for a session
func GetSSHSockPath(name string) (string, error) {
	dir, err := EnsureDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("%s.ssh_auth_sock", name)), nil
}

// IsAlive checks if the shell process is still running and the socket is active
func (i Info) IsAlive() bool {
	if i.PID <= 0 {
		return false
	}
	process, err := os.FindProcess(i.PID)
	if err != nil {
		return false
	}
	// Signal 0 checks for process existence
	if err := process.Signal(syscall.Signal(0)); err != nil {
		return false
	}

	// Double check socket liveness to handle PID reuse after reboot/crash
	dir, _ := EnsureDir()
	sockPath := filepath.Join(dir, i.Name+".sock")
	conn, err := net.DialTimeout("unix", sockPath, 50*time.Millisecond)
	if err != nil {
		// Socket file exists but no one is listening -> stale
		return false
	}
	_ = conn.Close()
	return true
}

// Cleanup removes all files associated with a session
func Cleanup(name string) {
	dir, _ := EnsureDir()
	_ = os.Remove(filepath.Join(dir, name+".sock"))
	_ = os.Remove(filepath.Join(dir, name+".info"))
	_ = os.Remove(filepath.Join(dir, name+".ssh_auth_sock"))
	
	// Remove all .log and .log.N files
	files, _ := os.ReadDir(dir)
	for _, f := range files {
		if f.Name() == name+".log" || (len(f.Name()) > len(name)+5 && f.Name()[:len(name)+5] == name+".log.") {
			_ = os.Remove(filepath.Join(dir, f.Name()))
		}
	}
}

// GetLogFiles returns a sorted list of all log files for a session (oldest to newest)
func GetLogFiles(name string) ([]string, error) {
	dir, err := EnsureDir()
	if err != nil {
		return nil, err
	}
	
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	type logEntry struct {
		path  string
		index int
	}
	var rotated []logEntry

	activeLog := filepath.Join(dir, name+".log")

	prefix := name + ".log."
	for _, f := range files {
		if len(f.Name()) > len(prefix) && f.Name()[:len(prefix)] == prefix {
			idx, err := strconv.Atoi(f.Name()[len(prefix):])
			if err == nil {
				rotated = append(rotated, logEntry{filepath.Join(dir, f.Name()), idx})
			}
		}
	}

	// Sort by index (ascending = oldest to newest in this scheme)
	sort.Slice(rotated, func(i, j int) bool {
		return rotated[i].index < rotated[j].index
	})

	var result []string
	for _, lf := range rotated {
		result = append(result, lf.path)
	}
	
	// Active log is always newest
	if _, err := os.Stat(activeLog); err == nil {
		result = append(result, activeLog)
	}

	return result, nil
}

// Rename moves all session files to a new name
func Rename(oldName, newName string) error {
	dir, err := EnsureDir()
	if err != nil {
		return err
	}

	extensions := []string{".sock", ".info", ".log"}
	for _, ext := range extensions {
		oldPath := filepath.Join(dir, oldName+ext)
		newPath := filepath.Join(dir, newName+ext)
		if _, err := os.Stat(oldPath); err == nil {
			if err := os.Rename(oldPath, newPath); err != nil {
				return err
			}
		}
	}

	// Update the name inside the .info file
	info, err := ReadInfo(newName)
	if err == nil {
		info.Name = newName
		_ = WriteInfo(info)
	}

	return nil
}

// GetHomeDir returns the user's home directory
func GetHomeDir() (string, error) {
	return os.UserHomeDir()
}

// EnsureDir creates the persistent directory if it doesn't exist
func EnsureDir() (string, error) {
	home, err := GetHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, DirName)
	if err := os.MkdirAll(path, 0700); err != nil {
		return "", err
	}
	return path, nil
}

// GetSocketPath returns the path to the unix socket for a session
func GetSocketPath(name string) (string, error) {
	dir, err := EnsureDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("%s.sock", name)), nil
}

// GetLogPath returns the path to the log file for a session
func GetLogPath(name string) (string, error) {
	dir, err := EnsureDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("%s.log", name)), nil
}

// GetInfoPath returns the path to the info file for a session
func GetInfoPath(name string) (string, error) {
	dir, err := EnsureDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("%s.info", name)), nil
}

// WriteInfo writes session info to a file
func WriteInfo(info Info) error {
	path, err := GetInfoPath(info.Name)
	if err != nil {
		return err
	}
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// ReadInfo reads session info from a file
func ReadInfo(name string) (Info, error) {
	path, err := GetInfoPath(name)
	if err != nil {
		return Info{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Info{}, err
	}
	var info Info
	err = json.Unmarshal(data, &info)
	return info, err
}

// Clean removes all stale sessions and orphaned files
func Clean() (int, error) {
	dir, err := EnsureDir()
	if err != nil {
		return 0, err
	}

	files, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}

	// 1. Identify active sessions
	active := make(map[string]bool)
	for _, f := range files {
		if filepath.Ext(f.Name()) == ".info" {
			name := f.Name()[:len(f.Name())-5]
			info, err := ReadInfo(name)
			if err == nil && info.IsAlive() {
				active[name] = true
			}
		}
	}

	// 2. Remove files not belonging to active sessions
	removedCount := 0
	for _, f := range files {
		if f.IsDir() {
			continue
		}

		name := f.Name()
		var sessionName string
		isSessionFile := false

		if filepath.Ext(name) == ".sock" {
			sessionName = name[:len(name)-5]
			isSessionFile = true
		} else if filepath.Ext(name) == ".info" {
			sessionName = name[:len(name)-5]
			isSessionFile = true
		} else if len(name) > 14 && name[len(name)-14:] == ".ssh_auth_sock" {
			sessionName = name[:len(name)-14]
			isSessionFile = true
		} else if filepath.Ext(name) == ".log" {
			sessionName = name[:len(name)-4]
			isSessionFile = true
		} else {
			// Handle rotated logs: name.log.N
			// We look for ".log." inside the name
			re := regexp.MustCompile(`^(.*)\.log\.\d+$`)
			matches := re.FindStringSubmatch(name)
			if len(matches) > 1 {
				sessionName = matches[1]
				isSessionFile = true
			}
		}

		if isSessionFile && sessionName != "" && !active[sessionName] {
			fullPath := filepath.Join(dir, name)
			if err := os.Remove(fullPath); err == nil {
				removedCount++
			}
		}
	}
	return removedCount, nil
}

// List returns a list of active sessions
func List() ([]Info, error) {
	dir, err := EnsureDir()
	if err != nil {
		return nil, err
	}
	
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	
	var sessions []Info
	for _, f := range files {
		if filepath.Ext(f.Name()) == ".sock" {
			name := f.Name()[:len(f.Name())-5]
			info, err := ReadInfo(name)
			if err != nil {
				// If we can't read info, we can't verify PID. 
				// We assume it might be stale.
				Cleanup(name)
				continue
			}
			
			if info.IsAlive() {
				sessions = append(sessions, info)
			} else {
				// Process is dead, clean up stale files
				Cleanup(name)
			}
		}
	}
	return sessions, nil
}
