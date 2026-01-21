# Persishtent Project Context

`persishtent` is a minimal, persistent shell proxy layer written in Go. It allows users to detach from and resume shell sessions without the complexity of a full multiplexer like `tmux` or `screen`.

## Project Overview

- **Core Purpose:** Provides session persistence by proxying a shell's PTY through a background daemon.
- **Architecture:** Client-Daemon model.
  - **Daemon:** Each session runs a background daemon that manages the PTY, logs output to disk, and listens on a Unix socket.
  - **Client:** A thin CLI that connects to the daemon, handles terminal raw mode, replays session history, and proxies I/O.
- **Key Features:**
  - Full session output replay upon reattachment.
  - "Native-like" feel with support for alternate buffers (e.g., `vim`, `top`) and graceful restoration of terminal state.
  - **Smart Session Management:** Auto-naming (simple numeric indices), auto-attach, nesting protection, and **interactive selection menu**.
  - **Shell Integration:** Prompt injection (`persh:name`) and window title updates via `init` scripts.
  - **Configuration:** Customizable via `~/.config/persishtent/config.json` (log limits, prompt prefix, detach key).
  - Read-only attachment mode.
  - SSH agent forwarding support via stable symlinks in `~/.persishtent/`.
  - Custom TLV (Type-Length-Value) protocol for IPC.

## Technology Stack

- **Language:** Go (1.25+)
- **Libraries:**
  - `github.com/creack/pty`: PTY allocation and management.
  - `golang.org/x/term`: Terminal raw mode and size handling.
  - `golang.org/x/sys`: Low-level system calls (Signals, Unix sockets).
- **Storage:** Session data is stored in `~/.persishtent/` (sockets, logs, and JSON metadata).

## Directory Structure

- `cmd/persishtent/`: Entry point (thin wrapper).
- `internal/cli/`: CLI command implementation and helper logic.
- `internal/config/`: Configuration loading and defaults.
- `internal/server/`: Daemon/Server logic (PTY management, broadcasting, `LogRotator`).
- `internal/client/`: Client logic (`SessionClient` struct, attachment, log replay, terminal synchronization).
- `internal/protocol/`: Definition of the TLV protocol and constants.
- `internal/session/`: Session lifecycle management (listing, validation, cleanup, metadata).
- `tests/`: Integration tests for end-to-end verification.

## Building and Running

### Prerequisites
- Go installed (version 1.25 or higher recommended).
- `golangci-lint` for linting.

### Build Commands
```bash
# Build the binary
go build -o persishtent cmd/persishtent/main.go
```

### Running Tests
```bash
# Run all unit tests
go test -v ./...

# Run tests with race detection
go test -v -race ./...

# Run integration tests
go test -v tests/integration_test.go
```

### Linting
```bash
# Run the linter
golangci-lint run
```

## Configuration

Configuration is loaded from `~/.config/persishtent/config.json`.

```json
{
  "log_rotation_size_mb": 1,
  "max_log_rotations": 5,
  "prompt_prefix": "persh",
  "detach_key": "ctrl-d"
}
```

## Commands

- `persishtent`: Auto-attach or interactive selection.
- `persishtent start [-d] [name]`: Start a new session.
- `persishtent attach [name]`: Attach to a session.
- `persishtent list`: List active sessions.
- `persishtent kill [name]`: Kill a session.
- `persishtent rename <old> <new>`: Rename a session.
- `persishtent clean`: Cleanup stale sockets and logs.
- `persishtent init <bash|zsh>`: Generate shell integration script.
- `persishtent completion`: Generate shell completion script.

## Development Conventions

- **Internal Packages:** Core logic is kept in `internal/` to encapsulate implementation details and prevent external imports.
- **Session Cleanup:** Stale sessions (dead PIDs or unreachable sockets) are automatically pruned on CLI invocation via `session.Clean()`.
- **Protocol Stability:** `internal/protocol/protocol.go` defines packet types and constants (`ModeMaster`, `ModeReadOnly`).
- **Log Rotation:** The daemon handles log rotation via `LogRotator` in `internal/server/logger.go`.