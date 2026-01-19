package session

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	DirName = ".persishtent"
)

// Info holds information about a persistent session
type Info struct {
	Name string
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

// List returns a list of active session names
func List() ([]string, error) {
	dir, err := EnsureDir()
	if err != nil {
		return nil, err
	}
	
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	
	var sessions []string
	for _, f := range files {
		if filepath.Ext(f.Name()) == ".sock" {
			sessions = append(sessions, f.Name()[:len(f.Name())-5])
		}
	}
	return sessions, nil
}
