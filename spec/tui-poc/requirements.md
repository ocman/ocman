# TUI PoC - Requirements

## Overview

A terminal-based UI for ocman that provides a split-pane interface: a
narrow sidebar listing discovered coding-agent instances with live
status, and a main pane containing a fully interactive, embedded
terminal running the selected instance's actual TUI/CLI.

Unlike the web dashboard, the TUI does not render its own view of
session data. Instead it **mounts the real terminal output** of each
running OpenCode or Claude Code instance inside the app, giving the
user a multiplexer-like experience purpose-built for coding agents.

The PoC validates the core technical bet: can libghostty-vt (Ghostty's
embeddable VT emulator, exposed as a C library) be used from a Go
Bubble Tea application to faithfully render a nested full-screen TUI
(OpenCode) or interactive CLI (Claude Code) inside a viewport, with
bidirectional input?

## Goals

1. Prove that libghostty-vt can be driven from Go via CGo to emulate
   a full terminal inside a Bubble Tea viewport — including alternate
   screen buffers, 24-bit color, cursor positioning, and styled text.
2. Provide a usable split-pane interface where the user can see all
   running instances at a glance and switch between them with a
   keystroke.
3. Reuse ocman's existing instance-discovery infrastructure (lsof-based
   port discovery for OpenCode, hook-based status for Claude Code) so
   the sidebar populates automatically with zero configuration.
4. Deliver a self-contained PoC binary (`ocman tui`) that can be
   evaluated independently of the web dashboard.

## Non-Goals (PoC scope)

- **Not replacing the web dashboard.** The TUI is a complementary
  interface, not a substitute.
- **No session history or message browsing.** The TUI shows live
  instances only; historical session data stays in the web UI.
- **No composer.** The user interacts with instances directly through
  the embedded terminal — they type into OpenCode's or Claude Code's
  own input, not an ocman-provided composer.
- **No database access.** The PoC does not read OpenCode's SQLite DB
  or ocman's state.db. All data comes from instance discovery and the
  embedded PTY streams.
- **No Claude Code hook listener.** The PoC discovers Claude Code
  instances via process scanning only; it does not install or listen
  for hooks. Hook-based status is a follow-up.
- **No Windows support.** PTY and lsof are Unix-only; same constraint
  as the rest of ocman.
- **No tests for the libghostty CGo bindings.** The bindings are
  thin wrappers validated by the PoC itself; formal test coverage is
  a follow-up once the approach is proven.

## Target Users

The ocman maintainer, running multiple concurrent OpenCode and/or
Claude Code sessions. The TUI is for users who prefer to stay in the
terminal and want a single pane-of-glass for all their agent sessions
without leaving their terminal multiplexer or opening a browser.

## Functional Requirements

### FR-1: Instance discovery

- **Description**: The TUI automatically discovers running coding-agent
  instances on the local machine.
- **Acceptance Criteria**:
  - OpenCode instances are discovered via the existing `lsof`-based
    port scanner (`internal/platforms/opencode/client.go`). Each
    discovered instance is identified by its working directory and
    port.
  - Claude Code instances are discovered by scanning for `claude`
    processes (via `lsof` or `ps`), filtering by argv to exclude
    `Claude.app` desktop helpers.
  - Discovery runs on a configurable interval (default 10 seconds).
  - Newly discovered instances appear in the sidebar without user
    action. Instances that stop running are removed (or marked
    offline) on the next scan.

### FR-2: Sidebar instance list

- **Description**: A narrow left pane displays all discovered instances
  with their status.
- **Acceptance Criteria**:
  - Each row shows: platform icon/label (OpenCode or Claude Code),
    project directory (basename or short path), and status indicator
    (busy / idle / done / error / offline).
  - The currently selected instance is visually highlighted.
  - Navigation: `j`/`k` or arrow keys to move selection; `Enter` to
    focus the main pane (passthrough mode).
  - The sidebar is scrollable when the instance count exceeds the
    visible height.
  - Status is derived from:
    - OpenCode: lightweight HTTP probe to the instance's port
      (`GET /session` or similar) to determine if it's busy/idle.
    - Claude Code: process-existence check (running = busy/idle;
      not running = offline). Finer status requires hooks (out of
      PoC scope).

### FR-3: Embedded terminal (main pane)

- **Description**: The right pane renders the full terminal output of
  the selected instance, using libghostty-vt as the VT emulator.
- **Acceptance Criteria**:
  - When an instance is selected, the TUI spawns the corresponding
    process attached to a PTY (pseudo-terminal):
    - OpenCode: `opencode` (or connects to an already-running
      instance if feasible).
    - Claude Code: `claude` (interactive mode).
  - Raw PTY output is fed into a libghostty-vt terminal instance via
    `ghostty_terminal_vt_write()`.
  - The Bubble Tea render loop reads the virtual screen (cells with
    codepoints, foreground/background colors, and style flags) from
    the libghostty render state and converts them to styled terminal
    output for the outer terminal.
  - The embedded terminal correctly renders:
    - Alternate screen buffer (OpenCode's TUI uses it).
    - 24-bit (truecolor) foreground and background.
    - Bold, italic, underline, strikethrough attributes.
    - Cursor positioning and visibility.
    - Unicode characters including multi-width (CJK) and emoji.
  - The embedded terminal resizes when the outer terminal or the
    Bubble Tea viewport resizes. Resize events propagate as
    `SIGWINCH` to the PTY and `ghostty_terminal_resize()` to the
    virtual terminal.

### FR-4: Input routing

- **Description**: The TUI has two input modes — sidebar mode and
  passthrough mode — with a clear toggle between them.
- **Acceptance Criteria**:
  - **Sidebar mode** (default): keyboard input is handled by the
    Bubble Tea model. Arrow keys navigate the instance list. `Enter`
    switches to passthrough mode. `q` quits the app.
  - **Passthrough mode**: all keyboard input is forwarded to the
    embedded terminal's PTY via the libghostty key encoder
    (`ghostty_key_encoder_encode()`). The user interacts with the
    real OpenCode/Claude Code TUI as if it were running directly.
  - **Escape hatch**: a configurable key combination (e.g. `Ctrl+\`
    or `Ctrl+]`) returns from passthrough mode to sidebar mode.
    This key combination is intercepted before being forwarded to
    the PTY.
  - The current mode is indicated visually (e.g. sidebar border
    color changes, or a small mode indicator).

### FR-5: Instance lifecycle management

- **Description**: The TUI manages the lifecycle of embedded terminal
  processes.
- **Acceptance Criteria**:
  - When the user selects a new instance, the previous instance's
    terminal remains alive in the background (not killed). Switching
    back restores its last-rendered state from the libghostty virtual
    screen.
  - When the TUI exits (`q` from sidebar mode or `Ctrl+C`), all
    spawned child processes are sent `SIGTERM`, then `SIGKILL` after
    a grace period (e.g. 3 seconds).
  - If a child process exits on its own (e.g. the user types `exit`
    in Claude Code), the instance is marked as "exited" in the
    sidebar and the main pane shows a message like "Process exited
    (code 0). Press Enter to respawn."
  - Respawning an exited instance creates a fresh PTY and process.

### FR-6: Launch mode

- **Description**: The TUI can either discover and attach to existing
  instances, or launch new ones.
- **Acceptance Criteria**:
  - **Discover mode** (default): the sidebar populates from the
    instance scanner. Selecting an instance spawns a new process in
    the same working directory (since attaching to an existing PTY
    owned by another terminal is not possible).
  - **Launch mode** (stretch goal): the user can press `n` in sidebar
    mode to launch a new instance, picking a directory from a list of
    known projects (reuse ocman's project index).
  - For discovered OpenCode instances: the TUI spawns
    `opencode --port 0` in the instance's working directory.
  - For discovered Claude Code instances: the TUI spawns `claude` in
    the instance's working directory.

## Non-Functional Requirements

### NFR-1: Rendering fidelity

- **Description**: The embedded terminal must render OpenCode's full
  TUI correctly — not just scrolling text, but the complete alternate-
  screen layout with panels, borders, and styled content.
- **Acceptance Criteria**:
  - Visual comparison: OpenCode running inside the TUI PoC is
    visually indistinguishable from OpenCode running directly in the
    same terminal, at the same dimensions.
  - No rendering artifacts from double-buffering, missed escape
    sequences, or incorrect color mapping.

### NFR-2: Input latency

- **Description**: Typing in passthrough mode must feel instantaneous.
- **Acceptance Criteria**:
  - Keypress-to-screen-update latency is under 50ms in typical
    conditions (measured subjectively; no formal benchmark required
    for PoC).
  - No dropped keystrokes under normal typing speed.

### NFR-3: Render performance

- **Description**: The render loop must keep up with fast-scrolling
  output (e.g. a build log or large code generation).
- **Acceptance Criteria**:
  - libghostty's dirty-row tracking is used to minimize per-frame
    work: only rows that changed since the last render are
    re-encoded.
  - The TUI does not visibly lag behind the actual process output
    during sustained high-throughput streaming.

### NFR-4: Build complexity

- **Description**: The CGo dependency on libghostty-vt must be
  manageable.
- **Acceptance Criteria**:
  - Pre-built static libraries (`.a`) for darwin-arm64 and
    darwin-amd64 are committed or fetched during build. Linux
    support is a follow-up.
  - The build is documented: a developer can build the TUI PoC with
    `make build-tui` (or equivalent) after installing the Zig
    toolchain (for building libghostty from source) or downloading
    the pre-built artifact.
  - The CGo bindings are isolated in a single package
    (`internal/tui/vt/`) so the rest of the codebase remains
    pure Go.

## Data Requirements

No persistent data. All state is ephemeral:

- Discovered instances (from lsof/process scanning).
- Per-instance libghostty terminal state (virtual screen buffer).
- Per-instance PTY file descriptors.
- Sidebar selection and input mode.

## Integration Points

- **`internal/platforms/opencode/client.go`**: reuse
  `discoverOpenCodePorts()` for OpenCode instance discovery.
- **`creack/pty`** (new Go dependency): PTY allocation and management
  for spawned child processes.
- **`libghostty-vt`** (new C dependency via CGo): virtual terminal
  emulation. Linked as a static library.
- **`charmbracelet/bubbletea`** (new Go dependency): TUI framework
  for the outer application shell (sidebar, layout, input routing).
- **`charmbracelet/lipgloss`** (new Go dependency): terminal styling
  for the sidebar and chrome.
- **`charmbracelet/bubbles`** (new Go dependency): reusable TUI
  components (list, viewport).

## Constraints

- **CGo required**: libghostty-vt is a C library. This breaks ocman's
  current pure-Go build for the TUI binary only. The web dashboard
  binary remains pure Go (no CGo). The two binaries may be built
  separately or gated behind a build tag.
- **Zig toolchain for building libghostty from source**: developers
  who want to build libghostty themselves need Zig installed. Pre-
  built artifacts avoid this for most users.
- **macOS / Linux only**: PTY allocation (`creack/pty`) and process
  discovery (`lsof`) are Unix-specific.
- **Cannot attach to existing PTYs**: Unix does not allow a process
  to "steal" another process's PTY. The TUI must spawn its own
  processes, even for already-running instances. This means the user
  gets a *new* OpenCode/Claude Code session in the same directory,
  not a view of the existing one.
- **Single terminal**: the TUI runs in a single terminal window. It
  does not use tmux, screen, or any external multiplexer.

## Assumptions

1. **libghostty-vt's C API is stable enough** for a PoC. The library
   is under active development; breaking changes are expected but
   acceptable for a proof-of-concept.
2. **Bubble Tea can render libghostty's cell grid efficiently.** The
   render loop converts cells to ANSI strings; this is the "decode
   VT to re-encode VT" overhead discussed in the architecture
   exploration. Assumed acceptable for PoC; profiling will confirm.
3. **OpenCode's TUI (Bubble Tea-based) renders correctly** inside a
   virtual terminal of the same dimensions. No known incompatibility,
   but this is the core bet of the PoC.
4. **`creack/pty` handles PTY allocation and SIGWINCH** on both macOS
   and Linux without issues.
5. **Pre-built libghostty-vt static libraries** can be produced for
   darwin-arm64 and darwin-amd64 from the Ghostty source tree using
   the Zig build system.

## Out of Scope

- **Web dashboard changes.** The TUI is additive; no web UI work.
- **Session history / message browsing.** View-only live terminals.
- **Composer / message injection.** Users type directly into the
  embedded terminal.
- **Claude Code hook installation / listener.** Status is process-
  based only.
- **Database access** (OpenCode DB or ocman state.db).
- **Windows support.**
- **Automated tests for CGo bindings.** Validated by the PoC itself.
- **Production packaging** (Homebrew formula, release binaries, etc.).
- **Mouse support** in the embedded terminal. Keyboard only for PoC;
  mouse passthrough is a follow-up.
- **Multiple simultaneous visible terminals** (split main pane). One
  terminal at a time; others are backgrounded.
- **Clipboard integration** between the embedded terminal and the
  host.

## Success Criteria

The PoC is successful if:

1. **OpenCode renders correctly**: launching `opencode` inside the
   TUI's embedded terminal produces a visually faithful reproduction
   of OpenCode's TUI, including the sidebar, message pane, composer
   input, and all styling.
2. **Interaction works**: the user can type a prompt into OpenCode's
   composer through the embedded terminal, see the streaming response,
   and approve/deny tool-use permissions — all without leaving the
   TUI.
3. **Switching works**: the user can press the escape key to return
   to the sidebar, select a different instance, and see that
   instance's terminal. Switching back restores the previous
   instance's state.
4. **No showstopper performance issues**: typing feels responsive,
   streaming output renders smoothly, and the TUI does not consume
   excessive CPU during idle periods.

If any of these fail, the PoC has identified a blocking limitation
of the approach, which is also a valid (negative) outcome.

## Open Questions

1. **Attach vs. spawn for OpenCode**: OpenCode instances discovered
   via lsof are already running with their own PTY. The TUI must
   spawn a *new* `opencode` process in the same directory. Should it
   use `opencode --port 0` (which starts a fresh session) or attempt
   to resume the most recent session? The PoC can start with fresh
   sessions and iterate.
2. **libghostty-vt build artifacts**: should pre-built `.a` files be
   committed to the repo (simple but bloats the repo) or fetched from
   a GitHub release (cleaner but adds a build dependency)? Architect
   to decide.
3. **Build tag isolation**: should the TUI binary be a separate
   `cmd/ocman-tui/main.go` entrypoint, or a subcommand of the main
   `ocman` binary behind a `//go:build cgo` tag? Separate binary is
   simpler for the PoC; subcommand is better long-term.
4. **Key combination for escape hatch**: `Ctrl+\` (SIGQUIT) is
   conventional but may conflict with some shells. `Ctrl+]` is used
   by telnet/SSH. `Escape Escape` (double-tap) is another option.
   Needs user testing.
5. **Render strategy**: should the Bubble Tea `View()` function
   re-render the entire cell grid every frame (simple, potentially
   slow), or use libghostty's dirty-row tracking to emit partial
   updates (complex, faster)? The PoC should start simple and
   optimize if needed.
