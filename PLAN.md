# Persishtent Plan

## Goal
Create a persistent shell proxy layer "persishtent" in Go that supports detach/resume without multiplexing.

## Roadmap

- [x] **Initialization**
    - [x] Initialize Go module (`go mod init`)
    - [x] Create structure for CLI entry point

- [x] **Core Logic - Session Management**
    - [x] Define `Session` struct (ID, PID, Socket/Path, LogPath)
    - [x] Implement session creation (spawning a shell)
    - [x] Implement session listing

- [x] **PTY & Shell Interaction**
    - [x] Use `github.com/creack/pty` to spawn shell
    - [x] Handle window resize events (SIGWINCH)
    - [x] Handle input/output copying

- [x] **Persistence & Output**
    - [x] Write shell output to a persistent log file
    - [x] On attach, replay log file to stdout

- [x] **Socket/IPC**
    - [x] Setup unix socket for communication or direct process control (start/attach)
    - [x] If using direct attach (no daemon), ensure process stays alive when client detaches. 
    - [x] `persishtent start` spawns a daemon process per session.

- [x] **CLI Commands**
    - [x] `persishtent` (default: list sessions or help)
    - [x] `persishtent new <name>` (Implemented as `start`)
    - [x] `persishtent attach <name>`
    - [x] `persishtent list`
    - [x] `persishtent kill <name>`

- [x] **Testing**
    - [x] Unit tests for session logic
    - [x] Unit tests for Protocol
    - [x] Integration test verifying persistence and detach/attach

- [x] **Refinement**

    - [x] Clean up help messages

    - [x] Ensure "native-like feel" (raw mode handling)

    - [x] Set detach shortcut to Ctrl+D
