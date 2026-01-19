package main

import (
	"flag"
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
		startSession(generateAutoName(), false, "")
		return
	}

	cmd := os.Args[1]

	switch cmd {
	case "start", "s":
		startCmd := flag.NewFlagSet("start", flag.ExitOnError)
		detach := startCmd.Bool("d", false, "Start in detached mode")
		sock := startCmd.String("s", "", "Custom socket path")
		startCmd.Parse(os.Args[2:])

		checkNesting()
		name := ""
		if startCmd.NArg() > 0 {
			name = startCmd.Arg(0)
		} else {
			name = generateAutoName()
		}
		startSession(name, *detach, *sock)

	case "attach", "a":
		attachCmd := flag.NewFlagSet("attach", flag.ExitOnError)
		sock := attachCmd.String("s", "", "Custom socket path")
		attachCmd.Parse(os.Args[2:])

		checkNesting()
		name := ""
		if attachCmd.NArg() > 0 {
			name = attachCmd.Arg(0)
		} else {
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
		}
		attachSession(name, *sock)

	case "kill", "k":
		killCmd := flag.NewFlagSet("kill", flag.ExitOnError)
		sock := killCmd.String("s", "", "Custom socket path")
		killCmd.Parse(os.Args[2:])

		name := ""
		if killCmd.NArg() > 0 {
			name = killCmd.Arg(0)
		} else {
			fmt.Println("Usage: persishtent kill [-s socket] <name>")
			return
		}

		if err := client.Kill(name, *sock); err != nil {
			fmt.Printf("Error killing session '%s': %v\n", name, err)
		} else {
			fmt.Printf("Session '%s' killed.\n", name)
		}

	case "daemon": // Internal
		daemonCmd := flag.NewFlagSet("daemon", flag.ExitOnError)
		sock := daemonCmd.String("s", "", "Custom socket path")
		daemonCmd.Parse(os.Args[2:])

		if daemonCmd.NArg() < 1 {
			return
		}
		name := daemonCmd.Arg(0)
		// Daemon runs until shell exits
		if err := server.Run(name, *sock); err != nil {
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
			attachSession(cmd, "")
		} else {
			startSession(cmd, false, "")
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

func startSession(name string, detach bool, sockPath string) {
	// 1. Check if already exists
	checkPath := sockPath
	if checkPath == "" {
		checkPath, _ = session.GetSocketPath(name)
	}

	if _, err := os.Stat(checkPath); err == nil {
		if detach {
			fmt.Printf("Session '%s' already exists.\n", name)
			return
		}
		attachSession(name, sockPath)
		return
	}

	// 2. Spawn daemon
	exe, err := os.Executable()
	if err != nil {
		fmt.Println("Error finding executable:", err)
		return
	}

	args := []string{"daemon"}
	if sockPath != "" {
		args = append(args, "-s", sockPath)
	}
	args = append(args, name)

	cmd := exec.Command(exe, args...)
	// Detach process
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	if err := cmd.Start(); err != nil {
		fmt.Println("Error starting session:", err)
		return
	}

	if detach {
		fmt.Printf("Session '%s' started in detached mode.\n", name)
		return
	}

	// 3. Attach with retry
	// Wait for socket to appear
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(checkPath); err == nil {
			attachSession(name, sockPath)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Println("Timed out waiting for session to start.")
}

func attachSession(name string, sockPath string) {
	fmt.Print("\x1b[H\x1b[2J")
	fmt.Printf("[attaching to session '%s'. press ctrl+d, d to detach]\n", name)
	if err := client.Attach(name, sockPath); err != nil {
		if err == client.ErrDetached {
			fmt.Println("\n[detached]")
		} else {
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
	fmt.Println("  persishtent start (s) [flags] [name]")
	fmt.Println("    -d                             Start in detached mode")
	fmt.Println("    -s <path>                      Custom socket path")
	fmt.Println("  persishtent attach (a) [-s path] [name]")
	fmt.Println("  persishtent kill (k) [-s path] <name>")
	fmt.Println("")
	fmt.Println("Shortcuts:")
	fmt.Println("  Ctrl+D, d                        Detach from session")
	fmt.Println("  Ctrl+D, Ctrl+D                   Send Ctrl+D to session")
}
