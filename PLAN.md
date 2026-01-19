# Persishtent Plan

## Goal
Create a persistent shell proxy layer "persishtent" in Go that supports detach/resume without multiplexing.

## Roadmap

- [ ] **Initialization**
    - [x] Initialize Go module (`go mod init`)
    - [ ] Create structure for CLI entry point

- [ ] **Core Logic - Session Management**
    - [ ] Define `Session` struct (ID, PID, Socket/Path, LogPath)
    - [ ] Implement session creation (spawning a shell)
    - [ ] Implement session listing

- [ ] **PTY & Shell Interaction**
    - [ ] Use `github.com/creack/pty` to spawn shell
    - [ ] Handle window resize events (SIGWINCH)
    - [ ] Handle input/output copying

- [ ] **Persistence & Output**
    - [ ] Write shell output to a persistent log file
    - [ ] On attach, replay log file to stdout

- [ ] **Socket/IPC**
    - [ ] Setup unix socket for communication or direct process control (start/attach)
    - [ ] If using direct attach (no daemon), ensure process stays alive when client detaches. 
    - *Design Decision*: `persishtent` likely needs a server/daemon mode or a background process per session.
    - *Simpler Approach*: `persishtent start` spawns the PTY and the shell in the background (or keeps running as the master), and `persishtent attach` connects to it.
    - Let's go with: `persishtent session-name` starts a session or attaches if exists.

- [ ] **CLI Commands**
    - [ ] `persishtent` (default: list sessions or help)
    - [ ] `persishtent new <name>`
    - [ ] `persishtent attach <name>`
    - [ ] `persishtent list`
    - [ ] `persishtent kill <name>`

- [ ] **Testing**
    - [ ] Unit tests for session logic
    - [ ] Unit tests for PTY wrapper (mocking if possible)

- [ ] **Refinement**
    - [ ] Clean up help messages
    - [ ] Ensure "native-like feel" (raw mode handling)
