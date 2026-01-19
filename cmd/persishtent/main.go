package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"persishtent/internal/client"
	"persishtent/internal/server"
	"persishtent/internal/session"
)

func checkNesting() {
	if os.Getenv("PERSISHTENT_SESSION") != "" {
		fmt.Printf("[error: already inside a persishtent session (%s)]\n", os.Getenv("PERSISHTENT_SESSION"))
		os.Exit(1)
	}
}

func main() {
	if len(os.Args) < 2 {
		checkNesting()
		startSession(generateAutoName())
		return
	}

	cmd := os.Args[1]

	switch cmd {
	case "start", "s":
		checkNesting()
		var name string
		if len(os.Args) < 3 {
			name = generateAutoName()
		} else {
			name = os.Args[2]
		}
		startSession(name)
	case "attach", "a":
		checkNesting()
		var name string
		if len(os.Args) < 3 {
			sessions, err := session.List()
			if err != nil {
				fmt.Printf("Error checking sessions: %v\n", err)
				return
			}
			if len(sessions) == 1 {
				name = sessions[0].Name
			} else if len(sessions) == 0 {
				fmt.Println("No active sessions.")
				return
			} else {
				fmt.Println("Multiple sessions active. Please specify one:")
				for _, s := range sessions {
					fmt.Printf("  %s (pid: %d, cmd: %s)\n", s.Name, s.PID, s.Command)
				}
				return
			}
		} else {
			name = os.Args[2]
		}
		attachSession(name)
	case "kill", "k":
		if len(os.Args) < 3 {
			fmt.Println("Usage: persishtent kill <name>")
			return
		}
		if err := client.Kill(os.Args[2]); err != nil {
			fmt.Printf("Error killing session '%s': %v\n", os.Args[2], err)
		} else {
			fmt.Printf("Session '%s' killed.\n", os.Args[2])
		}
	case "daemon": // Internal
		if len(os.Args) < 3 {
			return
		}
		// Daemon runs until shell exits
		if err := server.Run(os.Args[2]); err != nil {
			os.Exit(1)
		}
	case "list", "ls":
		listSessions()
	case "help":
		printHelp()
	default:
		// Treat as attach/start shortcut
		checkNesting()
		// Check if session exists
		sock, _ := session.GetSocketPath(cmd)
		if _, err := os.Stat(sock); err == nil {
			attachSession(cmd)
		} else {
			// Ask or just start?
			// Let's just start for convenience
			startSession(cmd)
		}
	}
}

func generateAutoName() string {
	sessions, _ := session.List()
	used := make(map[string]bool)
	for _, s := range sessions {
		used[s.Name] = true
	}

	i := 0
	for {
		name := fmt.Sprintf("s%d", i)
		if !used[name] {
			return name
		}
		i++
	}
}

func startSession(name string) {
	// 1. Check if already exists
	sock, _ := session.GetSocketPath(name)
	if _, err := os.Stat(sock); err == nil {
		attachSession(name)
		return
	}

	// 2. Spawn daemon
	exe, err := os.Executable()
	if err != nil {
		fmt.Println("Error finding executable:", err)
		return
	}

	cmd := exec.Command(exe, "daemon", name)
	// Detach process
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	// We don't want the daemon to inherit our stdin/stdout
	// But we might want to capture stderr for debugging?
	// For now, dev null.
	// Actually, server.Run logs to .log file (via PTY output), but internal errors?
	// Maybe daemon should log internal errors to a separate file.
	// For MVP, we let it fly.

	if err := cmd.Start(); err != nil {
		fmt.Println("Error starting session:", err)
		return
	}

	// 3. Attach with retry
	// Wait for socket to appear
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(sock); err == nil {
			attachSession(name)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Println("Timed out waiting for session to start.")
}

func attachSession(name string) {
	fmt.Printf("[attaching to session '%s'. press ctrl+d, d to detach]\n", name)
	if err := client.Attach(name); err != nil {
		if err == client.ErrDetached {
			fmt.Println("\n[detached]")
		} else {
			// If attach fails (e.g. connection refused), maybe the socket is stale?
			// We could check that.
			fmt.Printf("[error attaching to '%s': %v]\n", name, err)
		}
	} else {
		fmt.Println("\n[terminated]")
	}
}

func listSessions() {
	current := os.Getenv("PERSISHTENT_SESSION")
	sessions, err := session.List()
	if err != nil {
		fmt.Printf("Error listing sessions: %v\n", err)
		return
	}
	if len(sessions) == 0 {
		fmt.Println("No active sessions.")
		return
	}
	fmt.Println("Active sessions:")
	for _, s := range sessions {
		prefix := "  "
		if s.Name == current {
			prefix = "* "
		}
		fmt.Printf("%s%s (pid: %d, cmd: %s)\n", prefix, s.Name, s.PID, s.Command)
	}
}

func printHelp() {
	fmt.Println("persishtent - persistent shell proxy")
	fmt.Println("Usage:")
	fmt.Println("  persishtent                      Start a new auto-named session")
	fmt.Println("  persishtent <name>               Start or attach to session")
	fmt.Println("  persishtent list (ls)            List active sessions")
	fmt.Println("  persishtent start (s) [name]     Start a new session (auto-named s0, s1... if omitted)")
	fmt.Println("  persishtent attach (a) [name]    Attach to an existing session (auto-selects if only one)")
	fmt.Println("  persishtent kill (k) <name>      Kill an active session")
	fmt.Println("")
	fmt.Println("Shortcuts:")
	fmt.Println("  Ctrl+D, d                        Detach from session")
	fmt.Println("  Ctrl+D, Ctrl+D                   Send Ctrl+D to session")
}
