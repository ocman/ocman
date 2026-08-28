package local

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

const (
	beadsTimeout   = 5 * time.Second
	beadsCacheTTL  = 10 * time.Second
	beadsStdoutCap = 1 << 20
	beadsStderrCap = 8 << 10
)

type beadsCacheEntry struct {
	status    hostsvc.BeadsStatus
	expiresAt time.Time
}

type beadsRunner interface {
	LookPath(string) (string, error)
	Run(context.Context, string, string, []string, []string) ([]byte, []byte, error)
}

type execBeadsRunner struct{}

func (execBeadsRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (execBeadsRunner) Run(ctx context.Context, path, dir string, args, env []string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr limitedBuffer
	stdout.remaining = beadsStdoutCap
	stderr.remaining = beadsStderrCap
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if stdout.overflow || stderr.overflow {
		return stdout.Bytes(), stderr.Bytes(), errors.New("beads output exceeded limit")
	}
	return stdout.Bytes(), stderr.Bytes(), err
}

type limitedBuffer struct {
	bytes.Buffer
	remaining int
	overflow  bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if n > b.remaining {
		_, _ = b.Buffer.Write(p[:b.remaining])
		b.remaining = 0
		b.overflow = true
		return n, nil
	}
	b.remaining -= n
	_, _ = b.Buffer.Write(p)
	return n, nil
}

func (h *Host) BeadsStatus(ctx context.Context, dir string) (hostsvc.BeadsStatus, error) {
	if cached, ok := h.cachedBeadsStatus(dir); ok {
		return cached, nil
	}
	result := h.beadsSF.DoChan(dir, func() (any, error) {
		if cached, ok := h.cachedBeadsStatus(dir); ok {
			return cached, nil
		}
		status, err := h.readBeadsStatus(context.WithoutCancel(ctx), dir)
		if err != nil {
			return status, err
		}
		now := time.Now()
		h.beadsMu.Lock()
		for key, cached := range h.beadsCache {
			if !now.Before(cached.expiresAt) {
				delete(h.beadsCache, key)
			}
		}
		h.beadsCache[dir] = beadsCacheEntry{status: status, expiresAt: now.Add(beadsCacheTTL)}
		h.beadsMu.Unlock()
		return status, nil
	})
	select {
	case value := <-result:
		if value.Err != nil {
			return hostsvc.BeadsStatus{}, value.Err
		}
		return value.Val.(hostsvc.BeadsStatus), nil
	case <-ctx.Done():
		return hostsvc.BeadsStatus{}, ctx.Err()
	}
}

func (h *Host) cachedBeadsStatus(dir string) (hostsvc.BeadsStatus, bool) {
	h.beadsMu.Lock()
	defer h.beadsMu.Unlock()
	cached, ok := h.beadsCache[dir]
	if !ok || !time.Now().Before(cached.expiresAt) {
		delete(h.beadsCache, dir)
		return hostsvc.BeadsStatus{}, false
	}
	return cached.status, true
}

func (h *Host) readBeadsStatus(ctx context.Context, dir string) (hostsvc.BeadsStatus, error) {
	path, err := h.beadsRunner.LookPath("bd")
	if err != nil {
		return hostsvc.BeadsStatus{}, nil
	}
	version, err := h.runBeads(ctx, path, dir, []string{"version", "--json"}, []string{"BD_JSON_ENVELOPE=0"})
	if err != nil {
		return hostsvc.BeadsStatus{}, err
	}
	if !supportedBeadsVersion(version) {
		return hostsvc.BeadsStatus{}, nil
	}

	where, err := h.runBeads(ctx, path, dir, []string{"--readonly", "where", "--json"}, []string{
		"BD_JSON_ENVELOPE=1", "BEADS_DIR=", "BEADS_DB=", "BD_DB=",
	})
	if err != nil {
		if beadsWorkspaceMissing(where) {
			return hostsvc.BeadsStatus{}, nil
		}
		return hostsvc.BeadsStatus{}, err
	}
	if !validBeadsWorkspace(where) {
		return hostsvc.BeadsStatus{}, nil
	}

	out, err := h.runBeads(ctx, path, dir, []string{"-C", dir, "--readonly", "list", "--json"}, []string{"BD_JSON_ENVELOPE=0"})
	if err != nil {
		return hostsvc.BeadsStatus{Available: true, Error: "status_unavailable"}, nil
	}
	tickets, ok := parseBeadsTickets(out)
	if !ok {
		return hostsvc.BeadsStatus{}, nil
	}
	result := hostsvc.BeadsStatus{Available: true, Tickets: tickets}
	if len(tickets) < 2 {
		return result, nil
	}

	args := []string{"-C", dir, "--readonly", "dep", "list"}
	for _, ticket := range tickets {
		args = append(args, ticket.ID)
	}
	args = append(args, "--type", "parent-child", "--json")
	deps, err := h.runBeads(ctx, path, dir, args, []string{"BD_JSON_ENVELOPE=0"})
	if err != nil {
		result.Error = "status_unavailable"
		return result, nil
	}
	if !applyBeadsParents(result.Tickets, deps) {
		return hostsvc.BeadsStatus{}, nil
	}
	return result, nil
}

func supportedBeadsVersion(data []byte) bool {
	var result struct {
		Version string `json:"version"`
	}
	if !decodeBeadsJSON(data, &result) {
		return false
	}
	parts := strings.Split(result.Version, ".")
	if len(parts) != 3 {
		return false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	_, err = strconv.Atoi(parts[2])
	return err == nil && (major > 1 || major == 1 && minor >= 1)
}

func (h *Host) runBeads(parent context.Context, path, dir string, args, env []string) ([]byte, error) {
	out, _, err := h.runBeadsOutput(parent, path, dir, args, env)
	return out, err
}

func (h *Host) runBeadsOutput(parent context.Context, path, dir string, args, env []string) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(parent, beadsTimeout)
	defer cancel()
	out, stderr, err := h.beadsRunner.Run(ctx, path, dir, args, env)
	if err != nil && len(stderr) > 0 {
		log.WithError(err).WithField("stderr", strings.TrimSpace(string(stderr))).Debug("beads command failed")
	}
	return out, stderr, err
}

func beadsWorkspaceMissing(stdout []byte) bool {
	var result struct {
		Error string `json:"error"`
		Data  struct {
			Error string `json:"error"`
		} `json:"data"`
	}
	return decodeBeadsJSON(stdout, &result) && (result.Error == "no_beads_directory" || result.Data.Error == "no_beads_directory")
}

func validBeadsWorkspace(data []byte) bool {
	var envelope struct {
		SchemaVersion int `json:"schema_version"`
		Data          struct {
			Path string `json:"path"`
		} `json:"data"`
	}
	return decodeBeadsJSON(data, &envelope) && envelope.SchemaVersion == 1 && envelope.Data.Path != ""
}

func parseBeadsTickets(data []byte) ([]hostsvc.BeadsTicket, bool) {
	var rows []struct {
		ID        *string `json:"id"`
		Title     *string `json:"title"`
		Status    *string `json:"status"`
		Priority  *int    `json:"priority"`
		IssueType string  `json:"issue_type"`
	}
	if !decodeBeadsJSON(data, &rows) || rows == nil {
		return nil, false
	}
	tickets := make([]hostsvc.BeadsTicket, 0, len(rows))
	for _, row := range rows {
		if row.ID == nil || *row.ID == "" || row.Title == nil || *row.Title == "" || row.Status == nil || row.Priority == nil ||
			*row.Priority < 0 || *row.Priority > 4 ||
			!slices.Contains([]string{"open", "in_progress", "blocked", "deferred"}, *row.Status) {
			return nil, false
		}
		tickets = append(tickets, hostsvc.BeadsTicket{ID: *row.ID, Title: *row.Title, Status: *row.Status, Priority: *row.Priority, IssueType: row.IssueType})
	}
	return tickets, true
}

func applyBeadsParents(tickets []hostsvc.BeadsTicket, data []byte) bool {
	var deps []struct {
		IssueID     *string `json:"issue_id"`
		DependsOnID *string `json:"depends_on_id"`
		Type        *string `json:"type"`
	}
	if !decodeBeadsJSON(data, &deps) || deps == nil {
		return false
	}
	indices := make(map[string]int, len(tickets))
	for i := range tickets {
		indices[tickets[i].ID] = i
	}
	for _, dep := range deps {
		if dep.IssueID == nil || dep.DependsOnID == nil || dep.Type == nil || *dep.Type != "parent-child" {
			return false
		}
		child, childOK := indices[*dep.IssueID]
		_, parentOK := indices[*dep.DependsOnID]
		if childOK && parentOK {
			tickets[child].ParentID = *dep.DependsOnID
		}
	}
	for i := range tickets {
		seen := map[string]bool{}
		for id := tickets[i].ID; id != ""; {
			if seen[id] {
				tickets[i].ParentID = ""
				break
			}
			seen[id] = true
			parent, ok := indices[id]
			if !ok {
				break
			}
			id = tickets[parent].ParentID
		}
	}
	return true
}

func decodeBeadsJSON(data []byte, value any) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(value); err != nil {
		return false
	}
	return decoder.Decode(&struct{}{}) == io.EOF
}
