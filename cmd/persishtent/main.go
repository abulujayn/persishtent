package main

import (
	"flag"
	"fmt"
	"os"

	"persishtent/internal/cli"
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
			cli.AttachSession(sessions[0].Name, "", true, false, 0)
		} else if len(sessions) == 0 {
			cli.StartSession(cli.GenerateAutoName(), false, "", "", true, false, "")
		} else {
			name := cli.SelectSession(sessions)
			if name != "" {
				cli.AttachSession(name, "", true, false, 0)
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
			name = cli.GenerateAutoName()
		}
		if err := session.ValidateName(name); err != nil {
			fmt.Printf("Error: %v\n", err)
			return
		}
		cli.StartSession(name, *detach, *sock, *command, true, *readOnly, *log)

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
				name = cli.SelectSession(sessions)
				if name == "" {
					return
				}
			}
		}
		cli.AttachSession(name, *sock, !*noReplay, *readOnly, *tail)

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
		cli.ListSessions()
	case "clean":
		_, count, err := session.Clean()
		if err != nil {
			fmt.Printf("Error cleaning sessions: %v\n", err)
		} else {
			fmt.Printf("Cleaned up %d stale files.\n", count)
		}
	case "completion":
		cli.PrintCompletionScript()
	case "init":
		if len(os.Args) < 3 {
			fmt.Println("Usage: persishtent init <bash|zsh>")
			return
		}
		cli.PrintInitScript(os.Args[2])
	case "help":
		cli.PrintHelp()
	default:
		// Treat as attach/start shortcut
		checkNesting()
		// Check if session exists
		sock, _ := session.GetSocketPath(cmd)
		if _, err := os.Stat(sock); err == nil {
			cli.AttachSession(cmd, "", true, false, 0)
		} else {
			cli.StartSession(cmd, false, "", "", true, false, "")
		}
	}
}
