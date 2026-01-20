package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/term"

	"persishtent/internal/client"
	"persishtent/internal/config"
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
	// Load config
	if err := config.Load(); err != nil {
		fmt.Printf("Warning: failed to load config: %v\n", err)
	}

	// Auto-prune stale sessions on every invocation
	sessions, _, _ := session.Clean()

	if len(os.Args) < 2 {
		checkNesting()
		if len(sessions) == 1 {
			attachSession(sessions[0].Name, "", true, false, 0)
		} else if len(sessions) == 0 {
			startSession(generateAutoName(), false, "", "", true, false, "")
		} else {
			name := selectSession(sessions)
			if name != "" {
				attachSession(name, "", true, false, 0)
			}
		}
		return
	}

	cmd := os.Args[1]

	switch cmd {
	case "start", "s":
		startCmd := flag.NewFlagSet("start", flag.ExitOnError)
		detach := startCmd.Bool("d", false, "Start in detached mode")
		sock := startCmd.String("s", "", "Custom socket path")
		log := startCmd.String("l", "", "Custom log path")
		command := startCmd.String("c", "", "Custom command to run")
		readOnly := startCmd.Bool("ro", false, "Start in read-only mode")
		_ = startCmd.Parse(os.Args[2:])

		checkNesting()
		name := ""
		if startCmd.NArg() > 0 {
			name = startCmd.Arg(0)
		} else {
			name = generateAutoName()
		}
		if err := session.ValidateName(name); err != nil {
			fmt.Printf("Error: %v\n", err)
			return
		}
		startSession(name, *detach, *sock, *command, true, *readOnly, *log)

	case "attach", "a":
		attachCmd := flag.NewFlagSet("attach", flag.ExitOnError)
		sock := attachCmd.String("s", "", "Custom socket path")
		noReplay := attachCmd.Bool("n", false, "Do not replay session output")
		tail := attachCmd.Int("t", 0, "Only replay last N lines of output")
		readOnly := attachCmd.Bool("ro", false, "Attach in read-only mode")
		_ = attachCmd.Parse(os.Args[2:])

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
				name = selectSession(sessions)
				if name == "" {
					return
				}
			}
		}
		attachSession(name, *sock, !*noReplay, *readOnly, *tail)

	case "kill", "k":
		killCmd := flag.NewFlagSet("kill", flag.ExitOnError)
		all := killCmd.Bool("a", false, "Kill all sessions")
		sock := killCmd.String("s", "", "Custom socket path")
		_ = killCmd.Parse(os.Args[2:])

		if *all {
			sessions, _ := session.List()
			for _, s := range sessions {
				if err := client.Kill(s.Name, ""); err != nil {
					fmt.Printf("Error killing session '%s': %v\n", s.Name, err)
				} else {
					fmt.Printf("Session '%s' killed.\n", s.Name)
				}
			}
			return
		}

		name := ""
		if killCmd.NArg() > 0 {
			name = killCmd.Arg(0)
		} else {
			fmt.Println("Usage: persishtent kill [-a] [-s socket] <name>")
			return
		}

		if err := client.Kill(name, *sock); err != nil {
			fmt.Printf("Error killing session '%s': %v\n", name, err)
		} else {
			fmt.Printf("Session '%s' killed.\n", name)
		}

	case "rename", "r":
		if len(os.Args) < 4 {
			fmt.Println("Usage: persishtent rename <old> <new>")
			return
		}
		if err := session.ValidateName(os.Args[3]); err != nil {
			fmt.Printf("Error: %v\n", err)
			return
		}
		if err := session.Rename(os.Args[2], os.Args[3]); err != nil {
			fmt.Printf("Error renaming session: %v\n", err)
		} else {
			fmt.Printf("Session '%s' renamed to '%s'.\n", os.Args[2], os.Args[3])
		}

	case "daemon": // Internal
		daemonCmd := flag.NewFlagSet("daemon", flag.ExitOnError)
		sock := daemonCmd.String("s", "", "Custom socket path")
		log := daemonCmd.String("l", "", "Custom log path")
		command := daemonCmd.String("c", "", "Custom command")
		_ = daemonCmd.Parse(os.Args[2:])

		if daemonCmd.NArg() < 1 {
			return
		}
		name := daemonCmd.Arg(0)
		// Daemon runs until shell exits
		if err := server.Run(name, *sock, *log, *command); err != nil {
			os.Exit(1)
		}

	case "list", "ls":
		listSessions()
	case "clean":
		_, count, err := session.Clean()
		if err != nil {
			fmt.Printf("Error cleaning sessions: %v\n", err)
		} else {
			fmt.Printf("Cleaned up %d stale files.\n", count)
		}
	case "completion":
		printCompletionScript()
	case "help":
		printHelp()
	default:
		// Treat as attach/start shortcut
		checkNesting()
		// Check if session exists
		sock, _ := session.GetSocketPath(cmd)
		if _, err := os.Stat(sock); err == nil {
			attachSession(cmd, "", true, false, 0)
		} else {
			startSession(cmd, false, "", "", true, false, "")
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
		name := fmt.Sprintf("%d", i)
		if !used[name] {
			return name
		}
		i++
	}
}

func startSession(name string, detach bool, sockPath string, customCmd string, replay bool, readOnly bool, logPath string) {
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
		attachSession(name, sockPath, replay, readOnly, 0)
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
	if logPath != "" {
		args = append(args, "-l", logPath)
	}
	if customCmd != "" {
		args = append(args, "-c", customCmd)
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
			attachSession(name, sockPath, replay, readOnly, 0)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Println("Timed out waiting for session to start.")
}

func attachSession(name string, sockPath string, replay bool, readOnly bool, tail int) {
	fmt.Print("\x1b[H\x1b[2J")
	if readOnly {
		fmt.Printf("[attaching to session '%s' (READ-ONLY). press ctrl+d, d to detach]\n", name)
	} else {
		fmt.Printf("[attaching to session '%s'. press ctrl+d, d to detach]\n", name)
	}
	if err := client.Attach(name, sockPath, replay, readOnly, tail); err != nil {
		switch err {
		case client.ErrDetached:
			fmt.Println("\n[detached]")
		case client.ErrKicked:
			fmt.Println("\n[detached by another connection]")
		default:
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
		duration := time.Since(s.StartTime).Round(time.Second)
		fmt.Printf("%s%s (pid: %d, cmd: %s, up: %s)\n", prefix, s.Name, s.PID, s.Command, duration)
	}
}

func printHelp() {
	fmt.Println("persishtent - persistent shell proxy")
	fmt.Println("Usage:")
	fmt.Println("  persishtent                      Start a new auto-named session")
	fmt.Println("  persishtent <name>               Start or attach to session")
	fmt.Println("  persishtent list (ls)            List active sessions")
	fmt.Println("  persishtent clean                Clean up stale sessions and log files")
	fmt.Println("  persishtent completion           Generate shell completion script")
	fmt.Println("  persishtent start (s) [flags] [name]")
	fmt.Println("    -d                             Start in detached mode")
	fmt.Println("    -s <path>                      Custom socket path")
	fmt.Println("    -c <cmd>                       Custom command to run")
	fmt.Println("  persishtent attach (a) [flags] [name]")
	fmt.Println("    -n                             Do not replay session output")
	fmt.Println("    -t <n>                         Only replay last N lines of output")
	fmt.Println("    -ro                            Attach in read-only mode")
	fmt.Println("    -s <path>                      Custom socket path")
	fmt.Println("  persishtent kill (k) [flags] [name]")
	fmt.Println("    -a                             Kill all sessions")
	fmt.Println("    -s <path>                      Custom socket path")
	fmt.Println("  persishtent rename (r) <old> <new>")
	fmt.Println("")
	fmt.Println("Shortcuts:")
	fmt.Println("  Ctrl+D, d                        Detach from session")
	fmt.Println("  Ctrl+D, Ctrl+D                   Send Ctrl+D to session")
}

func printCompletionScript() {
	script := `#!/bin/bash
# Bash/Zsh completion for persishtent

_persishtent_completions() {
	local cur prev opts
	COMPREPLY=()
	cur="${COMP_WORDS[COMP_CWORD]}"
	prev="${COMP_WORDS[COMP_CWORD-1]}"
	opts="start attach list kill rename clean completion help"

	case "${prev}" in
		start|attach|kill|rename)
			local sessions=$(persishtent list 2>/dev/null | grep "^  " | awk '{print $1}')
			COMPREPLY=( $(compgen -W "${sessions}" -- ${cur}) )
			return 0
			;; 
		*)
			;;
	esac

	COMPREPLY=( $(compgen -W "${opts}" -- ${cur}) )
}

complete -F _persishtent_completions persishtent
`
	fmt.Print(script)
}

func selectSession(sessions []session.Info) string {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		// Fallback for non-interactive: print list and exit
		fmt.Println("Multiple sessions active. Please specify one:")
		for _, s := range sessions {
			fmt.Printf("  %s (pid: %d, cmd: %s)\n", s.Name, s.PID, s.Command)
		}
		return ""
	}

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return ""
	}
	defer func() { _ = term.Restore(int(os.Stdin.Fd()), oldState) }()

	idx := 0
	// Hide cursor
	fmt.Print("\x1b[?25l")
	defer fmt.Print("\x1b[?25h")

	first := true
	printList := func() {
		if !first {
			// Move up N+1 lines (N sessions + header)
			fmt.Printf("\x1b[%dA", len(sessions)+1)
		}
		first = false
		
		fmt.Printf("Select a session (Up/Down/Enter/q):\r\n")
		for i, s := range sessions {
			prefix := "   "
			if i == idx {
				prefix = " > "
			}
			fmt.Printf("%s%s (pid: %d, cmd: %s)\x1b[K\r\n", prefix, s.Name, s.PID, s.Command)
		}
	}

	printList()

	buf := make([]byte, 3)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return ""
		}
		
		if n == 1 {
			if buf[0] == 3 || buf[0] == 4 || buf[0] == 113 { // Ctrl+C, Ctrl+D, q
				return ""
			}
			if buf[0] == 13 || buf[0] == 10 { // Enter
				return sessions[idx].Name
			}
		} else if n == 3 && buf[0] == 27 && buf[1] == 91 {
			switch buf[2] {
			case 65: // Up
				if idx > 0 {
					idx--
					printList()
				}
			case 66: // Down
				if idx < len(sessions)-1 {
					idx++
					printList()
				}
			}
		}
	}
}