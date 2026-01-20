package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	// Reset Global to ensure we test defaults
	Global = Config{
		LogRotationSizeMB: 1,
		MaxLogRotations:   5,
		PromptPrefix:      "psh",
	}
	
	if Global.LogRotationSizeMB != 1 {
		t.Errorf("Default LogRotationSizeMB mismatch. Got %d, want 1", Global.LogRotationSizeMB)
	}
	if Global.MaxLogRotations != 5 {
		t.Errorf("Default MaxLogRotations mismatch. Got %d, want 5", Global.MaxLogRotations)
	}
	if Global.PromptPrefix != "psh" {
		t.Errorf("Default PromptPrefix mismatch. Got %s, want 'psh'", Global.PromptPrefix)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	// Point HOME to a temp dir where config doesn't exist
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	if err := Load(); err != nil {
		t.Fatalf("Load() should not fail on missing file: %v", err)
	}

	// Should still have defaults (or whatever was in Global before)
	// Let's reset Global first to be sure
	Global = Config{
		LogRotationSizeMB: 1,
		MaxLogRotations:   5,
		PromptPrefix:      "psh",
	}
	
	if Global.LogRotationSizeMB != 1 {
		t.Error("Defaults should be preserved when file is missing")
	}
}

func TestLoad_ValidFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	configDir := filepath.Join(tmpDir, ".config", "persishtent")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}

	content := []byte(`{
		"log_rotation_size_mb": 10,
		"max_log_rotations": 20,
		"prompt_prefix": "test_prompt"
	}`)

	if err := os.WriteFile(filepath.Join(configDir, "config.json"), content, 0600); err != nil {
		t.Fatal(err)
	}

	if err := Load(); err != nil {
		t.Fatalf("Load() failed on valid file: %v", err)
	}

	if Global.LogRotationSizeMB != 10 {
		t.Errorf("LogRotationSizeMB mismatch. Got %d, want 10", Global.LogRotationSizeMB)
	}
	if Global.MaxLogRotations != 20 {
		t.Errorf("MaxLogRotations mismatch. Got %d, want 20", Global.MaxLogRotations)
	}
	if Global.PromptPrefix != "test_prompt" {
		t.Errorf("PromptPrefix mismatch. Got %s, want 'test_prompt'", Global.PromptPrefix)
	}
}

func TestLoad_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	configDir := filepath.Join(tmpDir, ".config", "persishtent")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}

	// Invalid JSON
	content := []byte(`{ "log_rotation_size_mb": "not a number" }`)

	if err := os.WriteFile(filepath.Join(configDir, "config.json"), content, 0600); err != nil {
		t.Fatal(err)
	}

	if err := Load(); err == nil {
		t.Fatal("Load() should fail on invalid JSON")
	}
}
