package server

import (
	"net"
	"os/exec"
	"strings"

	log "github.com/sirupsen/logrus"

	claudecodeplatform "github.com/NoUseFreak/ocman/internal/platforms/claudecode"
)

// maybeInstallClaudeHooks refreshes the Claude Code hook block in
// ~/.claude/settings.json on every ocman launch, so hook payloads
// always come back to the address this process is actually listening
// on.
//
// No-op preconditions (all silent):
//   - The claude-code adapter is not registered.
//   - The `claude` CLI is not on PATH (i.e. the user hasn't installed
//     Claude Code — installing hooks would point at a CLI they
//     don't run).
//   - s.addr cannot be turned into a callback URL.
//
// All failure modes log a warning and return. Hook install is a
// best-effort enhancement — ocman must keep serving even when
// settings.json is unwritable (permissions) or malformed (user edit).
func (s *Server) maybeInstallClaudeHooks() {
	adapter, ok := s.registry.Get(claudecodeplatform.PlatformID)
	if !ok {
		// Ocman built / started without the claude-code adapter —
		// nothing to install.
		return
	}
	cc, ok := adapter.(*claudecodeplatform.Adapter)
	if !ok {
		log.Warn("claude-code adapter has unexpected go type; skipping hook install")
		return
	}

	// Presence check: the installer writes a curl command into
	// settings.json, so rewriting it on a box with no `claude` CLI
	// would be confusing dead config. `exec.LookPath` is cheap —
	// a single $PATH walk.
	if _, err := exec.LookPath("claude"); err != nil {
		log.Debug("claude CLI not on PATH; skipping hook install")
		return
	}

	url := hookURLFromAddr(s.addr)
	if url == "" {
		log.WithField("addr", s.addr).
			Warn("could not derive hook callback URL; skipping claude hook install")
		return
	}

	if err := cc.RefreshHooks(url); err != nil {
		// A broken settings.json (user hand-edit that no longer
		// parses) is the most likely reason to land here. Log the
		// path so the user can fix it.
		log.WithError(err).WithField("url", url).
			Warn("installing Claude Code hooks failed; live status will not update")
		return
	}
	log.WithField("url", url).Info("refreshed Claude Code hooks in ~/.claude/settings.json")
}

// hookURLFromAddr turns the server's -addr flag value into a full
// HTTP callback URL for Claude Code to POST hook events to.
//
// Rewrites any wildcard / any-interface host to 127.0.0.1 because the
// hook command runs in the user's own shell on the same machine, and
// the loopback interface is always reachable regardless of how ocman
// was bound.
//
// Returns "" when the addr cannot be parsed; callers should skip the
// install rather than produce a broken URL.
func hookURLFromAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	if port == "" {
		return ""
	}
	switch normaliseHost(host) {
	case "", "0.0.0.0", "::", "[::]", "localhost", "::1", "[::1]":
		host = "127.0.0.1"
	default:
		// Keep user-specified hostname. We still expect hook sources
		// to hit the loopback, but if the operator has intentionally
		// set an explicit host, don't override.
	}
	return "http://" + host + ":" + port + "/api/hooks/claude"
}

// normaliseHost strips any bracket wrappers from an IPv6 literal so
// the switch above can match on the bare form.
func normaliseHost(h string) string {
	h = strings.TrimPrefix(h, "[")
	h = strings.TrimSuffix(h, "]")
	return h
}
