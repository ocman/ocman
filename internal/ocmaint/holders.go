package ocmaint

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// Holder is a process other than ocman with the OpenCode database open.
type Holder struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
}

func (h Holder) String() string { return fmt.Sprintf("%s (pid %d)", h.Command, h.PID) }

// dbFiles lists the database and its WAL/SHM siblings that exist.
func dbFiles(path string) []string {
	var out []string
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// LsofHolders reports every process except this one holding any of the
// database files open.
func LsofHolders(ctx context.Context, dbPath string) ([]Holder, error) {
	files := dbFiles(dbPath)
	if len(files) == 0 {
		return nil, nil
	}
	out, err := exec.CommandContext(ctx, "lsof", append([]string{"-F", "pc", "--"}, files...)...).Output()
	// lsof exits 1 when no process has the files open.
	var exitErr *exec.ExitError
	if err != nil && (!errors.As(err, &exitErr) || exitErr.ExitCode() != 1) {
		return nil, fmt.Errorf("lsof: %w", err)
	}
	return parseLsof(string(out), os.Getpid()), nil
}

// parseLsof reads `lsof -F pc` output: a p<pid> line, then c<command>.
func parseLsof(out string, self int) []Holder {
	var holders []Holder
	seen := map[int]bool{}
	cur := -1 // index of the holder the next c line belongs to
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			cur = -1
			pid, err := strconv.Atoi(line[1:])
			if err != nil || pid == self || seen[pid] {
				continue
			}
			seen[pid] = true
			holders = append(holders, Holder{PID: pid})
			cur = len(holders) - 1
		case 'c':
			if cur >= 0 {
				holders[cur].Command = line[1:]
			}
		}
	}
	return holders
}

// FreeBytes reports the space available to this user on dir's volume.
func FreeBytes(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil //nolint:gosec,unconvert // Bsize is positive; its type differs per OS
}
