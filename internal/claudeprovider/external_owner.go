package claudeprovider

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// Owner observations are advisory, not locks. Separate files per process let
// the browser and project supervisors share observations without lost updates.
type externalOwner struct {
	PID int   `json:"pid"`
	At  int64 `json:"at"`
}

func (l *live) externalOwnerDir(id string) string {
	key := sha256.Sum256([]byte(filepath.Clean(l.opts.ConfigDir) + "\x00" + id))
	return filepath.Join(filepath.Dir(l.opts.StatePath), "claude-external", fmt.Sprintf("%x", key))
}

func (l *live) observeExternal(req providerprotocol.HookEvent) {
	if l.opts.StatePath == "" || req.Managed || req.PID < 0 {
		return
	}
	dir := l.externalOwnerDir(req.SessionID)
	path := filepath.Join(dir, strconv.Itoa(req.PID)+".json")
	if req.Event == "SessionEnd" {
		_ = os.Remove(path)
		return
	}
	if os.MkdirAll(dir, 0o700) != nil {
		return // A failed observation must never prevent continuation.
	}
	data, _ := json.Marshal(externalOwner{PID: req.PID, At: now()})
	f, err := os.CreateTemp(dir, ".owner-")
	if err != nil {
		return
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		_ = os.Rename(f.Name(), path)
	}
}

func (l *live) externalWarnings(id string) []platforms.SessionWarning {
	if l.opts.StatePath == "" {
		return nil
	}
	dir := l.externalOwnerDir(id)
	files, _ := os.ReadDir(dir)
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, f.Name())
		data, err := os.ReadFile(path)
		var owner externalOwner
		if err != nil || json.Unmarshal(data, &owner) != nil {
			continue
		}
		if !owner.alive() {
			continue
		}
		return []platforms.SessionWarning{{Kind: "external-session-owner",
			Message: "This session may still be open in another Claude instance. You can continue here, but both instances can modify the session. Stop only controls the turn started by ocman."}}
	}
	return nil
}

func (o externalOwner) alive() bool {
	if o.PID <= 0 {
		// Older hooks and unusual process trees have no usable PID. Recent
		// activity is only a hint; it must expire, even if SessionEnd was lost.
		return time.Since(time.UnixMilli(o.At)) < hookBusyWindow
	}
	err := syscall.Kill(o.PID, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Hook commands may have an intermediate shell. Walk a bounded number of
// ancestors instead of mistaking that short-lived shell for the Claude owner.
func hookClaudePID(ctx context.Context) int {
	return findClaudePID(ctx, os.Getppid(), func(ctx context.Context, pid int) (int, string, error) {
		out, err := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "ppid=,comm=").Output()
		fields := strings.Fields(string(out))
		if err != nil || len(fields) < 2 {
			return 0, "", fmt.Errorf("process identity unavailable")
		}
		parent, err := strconv.Atoi(fields[0])
		return parent, strings.Join(fields[1:], " "), err
	})
}

func findClaudePID(ctx context.Context, pid int, lookup func(context.Context, int) (int, string, error)) int {
	for depth := 0; depth < 6 && pid > 1 && ctx.Err() == nil; depth++ {
		parent, command, err := lookup(ctx, pid)
		if err != nil {
			return 0
		}
		if name := filepath.Base(command); name == "claude" || strings.HasPrefix(name, "claude-") || strings.Contains(filepath.ToSlash(command), "/claude/versions/") {
			return pid
		}
		if parent == pid {
			return 0
		}
		pid = parent
	}
	return 0
}
