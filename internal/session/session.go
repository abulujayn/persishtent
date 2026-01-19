package session

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
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
	DirName = ".persishtent"
)

// Info holds information about a persistent session
type Info struct {
	Name      string    `json:"name"`
	PID       int       `json:"pid"`
	Command   string    `json:"command"`
	StartTime time.Time `json:"start_time"`
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
	conn.Close()
	return true
}

// Cleanup removes all files associated with a session
func Cleanup(name string) {
	dir, _ := EnsureDir()
	_ = os.Remove(filepath.Join(dir, name+".sock"))
	_ = os.Remove(filepath.Join(dir, name+".info"))
	_ = os.Remove(filepath.Join(dir, name+".log"))
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
