package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	DirName = ".persishtent"
)

// Info holds information about a persistent session
type Info struct {
	Name    string `json:"name"`
	PID     int    `json:"pid"`
	Command string `json:"command"`
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
				// Fallback if info file is missing
				info = Info{Name: name}
			}
			sessions = append(sessions, info)
		}
	}
	return sessions, nil
}
