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
  - Smart session management (auto-naming, auto-attach, nesting protection).
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

- `cmd/persishtent/`: Entry point and CLI command parsing.
- `internal/server/`: Daemon/Server logic (PTY management, broadcasting, log rotation).
- `internal/client/`: Client logic (attachment, log replay, terminal synchronization).
- `internal/protocol/`: Definition of the TLV protocol used for client-daemon communication.
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

## Development Conventions

- **Internal Packages:** Core logic is kept in `internal/` to encapsulate implementation details and prevent external imports.
- **Error Handling:** Errors are handled explicitly. Custom error types like `ErrDetached` and `ErrKicked` are used in the client for specific attachment states.
- **Session Cleanup:** Stale sessions (dead PIDs or unreachable sockets) are automatically pruned on CLI invocation via `session.List()`.
- **Protocol Stability:** Changes to `internal/protocol/protocol.go` must be carefully managed as they affect communication between different versions of the client and daemon.
- **Terminal State:** The client is responsible for restoring terminal state (ESC sequences) upon detachment to ensure the host terminal isn't left in a broken state (e.g., inside an alternate buffer).
- **Log Rotation:** The daemon handles basic log rotation (up to 5 rotations of 1MB each) to prevent disk exhaustion.
