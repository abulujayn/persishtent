# persishtent

`persishtent` is a minimal, persistent shell proxy layer written in Go. It allows you to detach from and resume shell sessions without the complexity of a full multiplexer like `tmux` or `screen`. It focuses on a "native-like" feel by replaying the session log upon reattachment and handling terminal states (like the alternate buffer) gracefully.

## Features

- **Persistence:** Detach from a session and reattach later from any terminal.
- **Native Feel:** Full session output replay on attach preserves scrollback context.
- **Minimal Design:** No panes, windows, or complex keybindings. Just your shell.
- **Auto-naming:** Automatically generates session names (`s0`, `s1`, ...) if none provided.
- **Smart Attach:** Automatically attaches if only one active session exists.
- **Nesting Protection:** Prevents starting or attaching to sessions from within an active `persishtent` session.
- **Alternate Buffer Support:** Properly exits alternate buffer (e.g., `vim`, `top`) upon detachment to restore terminal state.
- **Session Metadata:** Tracks PID and command for active sessions.

## Installation

Ensure you have Go installed, then:

```bash
go build -o persishtent cmd/persishtent/main.go
```

## Usage

### Commands

| Command | Alias | Description |
|---------|-------|-------------|
| `persishtent` | - | Start a new auto-named session (e.g., `s0`). |
| `persishtent <name>` | - | Start or attach to a session named `<name>`. |
| `persishtent list` | `ls` | List active sessions with PID and command. |
| `persishtent start [flags] [name]` | `s` | Start a new session (auto-named if omitted). |
| `persishtent attach [flags] [name]` | `a` | Attach to an existing session (auto-selects if only one). |
| `persishtent kill [flags] [name]` | `k` | Forcefully terminate active sessions. |
| `persishtent rename <old> <new>` | `r` | Rename an existing session. |
| `persishtent help` | - | Show help message. |

### Flags

#### `start`
- `-d`: Start in detached mode.
- `-s <path>`: Custom socket path.
- `-c <cmd>`: Custom command to run.

#### `attach`
- `-n`: Do not replay session output.
- `-s <path>`: Custom socket path.

#### `kill`
- `-a`: Kill all active sessions.
- `-s <path>`: Custom socket path.


### Shortcuts

While attached to a session:

- `Ctrl+D, d`: Detach from the session (shell stays alive).
- `Ctrl+D, Ctrl+D`: Send a literal `Ctrl+D` to the shell.
- Type `exit` and Enter: Terminate the shell and the session.

## Design & Implementation

### Architecture

`persishtent` uses a client-daemon architecture:

1. **Daemon (Server):** Each session runs a background daemon that spawns a shell using a PTY (`github.com/creack/pty`). It listens on a Unix socket for client connections and logs all PTY output to a local file.
2. **Client:** The CLI acts as a thin proxy, forwarding your terminal's `Stdin` to the daemon and printing the daemon's output to `Stdout`.

### Persistence & Synchronization

- **Logging:** All output is written to `~/.persishtent/<name>.log`. When a client attaches, the log is replayed to ensure the terminal state is restored.
- **DSR/CPR Sync:** To prevent terminal response pollution (e.g., the `6c` artifact caused by Device Attribute queries during log replay), the client uses a Device Status Report (DSR) and Cursor Position Report (CPR) handshake to synchronize with the terminal before enabling full I/O.
- **IPC:** Communication happens via Unix sockets using a simple TLV (Type-Length-Value) protocol.

### Cleanup

Session data is stored in `~/.persishtent/`:
- `<name>.sock`: Unix socket for IPC.
- `<name>.log`: Persistent output log.
- `<name>.info`: JSON metadata (PID, Command).

Files are automatically cleaned up when the shell process exits.
