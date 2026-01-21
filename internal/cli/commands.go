package cli

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/term"

	"persishtent/internal/client"
	"persishtent/internal/session"
)

func GenerateAutoName() string {
	sessions, _ := session.List()
	var names []string
	for _, s := range sessions {
		names = append(names, s.Name)
	}
	return FindNextAutoName(names)
}

func FindNextAutoName(existingNames []string) string {
	used := make(map[string]bool)
	for _, name := range existingNames {
		used[name] = true
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

func StartSession(name string, detach bool, sockPath string, customCmd string, replay bool, readOnly bool, logPath string) {
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
		AttachSession(name, sockPath, replay, readOnly, 0)
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
			AttachSession(name, sockPath, replay, readOnly, 0)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Println("Timed out waiting for session to start.")
}

func AttachSession(name string, sockPath string, replay bool, readOnly bool, tail int) {
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

func ListSessions() {
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

func PrintHelp() {
	fmt.Println("persishtent - persistent shell proxy")
	fmt.Println("Usage:")
	fmt.Println("  persishtent                      Start a new auto-named session")
	fmt.Println("  persishtent <name>               Start or attach to session")
	fmt.Println("  persishtent list (ls)            List active sessions")
	fmt.Println("  persishtent clean                Clean up stale sessions and log files")
	fmt.Println("  persishtent completion           Generate shell completion script")
	fmt.Println("  persishtent init <shell>         Generate shell integration script (bash|zsh)")
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

func PrintCompletionScript() {
	script := `#!/bin/bash
# Bash/Zsh completion for persishtent

_persishtent_completions() {
	local cur prev opts
	COMPREPLY=()
	cur="${COMP_WORDS[COMP_CWORD]}"
	prev="${COMP_WORDS[COMP_CWORD-1]}"
	opts="start attach list kill rename clean completion init help"

	case "${prev}" in
		start|attach|kill|rename)
			local sessions=$(persishtent list 2>/dev/null | grep "^  " | awk '{print $1}')
			COMPREPLY=( $(compgen -W "${sessions}" -- ${cur}) )
			return 0
			;;*
		*)
			;;
	esac

	COMPREPLY=( $(compgen -W "${opts}" -- ${cur}) )
}

complete -F _persishtent_completions persishtent
`
	fmt.Print(script)
}

func PrintInitScript(shell string) {
	switch shell {
	case "bash":
		fmt.Print(`
if [ -n "$PERSISHTENT_SESSION" ]; then
    PROMPT_COMMAND='echo -ne "\033]0;persishtent: ${PERSISHTENT_SESSION}\007"'
fi
`)
	case "zsh":
		fmt.Print(`
if [ -n "$PERSISHTENT_SESSION" ]; then
    precmd() {
        print -Pn "\e]0;persishtent: ${PERSISHTENT_SESSION}\a"
    }
fi
`)
	default:
		fmt.Printf("# Unsupported shell: %s\n", shell)
	}
}

func SelectSession(sessions []session.Info) string {
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