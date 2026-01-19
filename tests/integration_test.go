package tests

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestIntegration(t *testing.T) {
	// Build binary
	// Assuming we are running from root or tests dir.
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "persishtent")
	
	cmd := exec.Command("go", "build", "-o", binPath, "../cmd/persishtent/main.go")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to build: %v\nOutput: %s", err, output)
	}
	
	sessionName := "integration-test"
	
	// Cleanup any previous run
	home, _ := os.UserHomeDir()
	sockPath := filepath.Join(home, ".persishtent", sessionName+".sock")
	logPath := filepath.Join(home, ".persishtent", sessionName+".log")
	_ = os.Remove(sockPath)
	
	// Pre-fill log to test truncation
	garbage := []byte("OLD_SESSION_DATA_SHOULD_BE_GONE")
	if err := os.WriteFile(logPath, garbage, 0600); err != nil {
		t.Fatalf("Failed to write garbage log: %v", err)
	}

	// Start Session (detached)
	startCmd := exec.Command(binPath, "start", sessionName)
	
	// Use pty to start the client, so it thinks it has a terminal
	ptmx, err := pty.Start(startCmd)
	if err != nil {
		t.Fatalf("Failed to start client with PTY: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	
	// Give it a moment to initialize
	time.Sleep(2 * time.Second)
	
	// Check if log was truncated
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}
	if string(content) == string(garbage) {
		t.Fatalf("Log file was not truncated! Content: %s", string(content))
	}
	// It might contain new data (shell prompt), but it shouldn't START with garbage unless the shell output matched it (unlikely)
	// or if it wasn't truncated. 
	// If it wasn't truncated, it would be "OLD...<new data>"
	// We can check if it contains the garbage.
	// Actually, if we use O_TRUNC, the file size becomes 0 then grows.
	// If we use O_APPEND, it stays and grows.
	// So we check if content contains the garbage string.
	// Ideally we check if it *starts* with it? No, because O_TRUNC wipes it.
	// So if we find the garbage string, it's a fail (unless the new shell randomly generated it).
	for i := 0; i < len(content)-len(garbage)+1; i++ {
		if string(content[i:i+len(garbage)]) == string(garbage) {
			t.Fatalf("Log file contains old data! Truncation failed.")
		}
	}
	
	// Send command
	markerFile := filepath.Join(tmpDir, "marker")
	_ = os.Remove(markerFile)
	
	// Command: echo 'hello persistent' > markerFile
	cmdStr := "echo 'hello persistent' > " + markerFile + "\n"
	if _, err := ptmx.Write([]byte(cmdStr)); err != nil {
		t.Fatalf("Failed to write to ptmx: %v", err)
	}
	
	time.Sleep(1 * time.Second)
	
	// Verify file exists (Persistence check 1: It works)
	if _, err := os.Stat(markerFile); os.IsNotExist(err) {
		t.Fatalf("Marker file was not created. Shell didn't execute command?")
	}
	
	// Now Detach (Kill the client process 'persishtent start')
	// The daemon should survive.
	if err := startCmd.Process.Kill(); err != nil {
		t.Logf("Failed to kill client: %v", err)
	}
	
	// Wait a bit
	time.Sleep(500 * time.Millisecond)
	
	// Verify socket still exists (daemon alive)
	if _, err := os.Stat(sockPath); os.IsNotExist(err) {
		t.Fatalf("Socket vanished after client detach. Daemon died?")
	}
	
	// Attach again (Persistence check 2: Re-attach)
	attachCmd := exec.Command(binPath, "attach", sessionName)
	
	// We need another PTY for the attached session
	attachPtmx, err := pty.Start(attachCmd)
	if err != nil {
		t.Fatalf("Failed to start attach with PTY: %v", err)
	}
	defer func() { _ = attachPtmx.Close() }()
	
	time.Sleep(1 * time.Second)
	// Exit the shell
	if _, err := attachPtmx.Write([]byte("exit\n")); err != nil {
		t.Logf("Failed to write exit: %v", err)
	}
	
	// Wait for attach to finish (daemon exits)
	// We can't wait on attachCmd directly because pty.Start doesn't return the same control
	// Actually attachCmd.Wait() works if we used pty.Start(attachCmd)
	attachCmd.Wait()
	
	// Check if socket is gone
	time.Sleep(1 * time.Second)
	if _, err := os.Stat(sockPath); err == nil {
		t.Fatalf("Socket still exists after exit command.")
	}

	// --- Test Kill Subcommand ---
	// Start another session
	killSessionName := "kill-test"
	killSockPath := filepath.Join(home, ".persishtent", killSessionName+".sock")
	_ = os.Remove(killSockPath)
	
	startKillCmd := exec.Command(binPath, "start", killSessionName)
	if err := startKillCmd.Start(); err != nil {
		t.Fatalf("Failed to start kill-test session: %v", err)
	}
	
	time.Sleep(2 * time.Second)
	
	// Verify it exists
	if _, err := os.Stat(killSockPath); os.IsNotExist(err) {
		t.Fatalf("kill-test session failed to start")
	}
	
	// Kill it using CLI
	killCmd := exec.Command(binPath, "kill", killSessionName)
	if out, err := killCmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to run kill command: %v, output: %s", err, out)
	}
	
	time.Sleep(1 * time.Second)
	
	// Verify it is gone
	if _, err := os.Stat(killSockPath); err == nil {
		t.Fatalf("Socket still exists after kill command for %s", killSessionName)
	}

	// --- Test Nesting Protection ---
	nestCmd := exec.Command(binPath, "list")
	nestCmd.Env = append(os.Environ(), "PERSISHTENT_SESSION=fake")
	out, err := nestCmd.CombinedOutput()
	if err == nil {
		t.Fatalf("Expected error when nesting sessions, but got none. Output: %s", out)
	}
	if !bytes.Contains(out, []byte("already inside a persishtent session")) {
		t.Fatalf("Unexpected error message for nesting protection: %s", out)
	}
}

