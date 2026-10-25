package claudeprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// 2.1.299 reports the fallback to default after an unsupported model switch.
func supportsNativeAuto(version string) bool {
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if len(parts) != 3 {
		return false
	}
	var v [3]int
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return false
		}
		v[i] = n
	}
	return v[0] > 2 || v[0] == 2 && (v[1] > 1 || v[1] == 1 && v[2] >= 299)
}

func nativeMode(mode string) string {
	if mode == "" {
		return "default"
	}
	return mode
}

func (l *live) checkSessionLaunch(o *owned) error {
	if l.foreign(o) {
		return l.foreignError(*o)
	}
	if o.NativeMode == "auto" && !l.nativeAuto {
		return fmt.Errorf("native Auto requires Claude Code 2.1.299 or newer: %w", platforms.ErrUnsupported)
	}
	return nil
}

func (l *live) serveNativePermission(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/sessions/") || !strings.HasSuffix(r.URL.Path, "/native-permission") {
		return false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/sessions/"), "/native-permission")
	if !l.nativeAuto {
		reply(w, nil, platforms.ErrUnsupported)
		return true
	}
	switch r.Method {
	case http.MethodGet:
		state, err := l.nativePermissionState(id)
		if err != nil {
			reply(w, nil, err)
		} else {
			writeJSON(w, http.StatusOK, state)
		}
	case http.MethodPost:
		var req platforms.SetPermissionRulesRequest
		err := decode(r, &req)
		if err == nil {
			err = l.setNativeMode(r.Context(), id, req.NativeMode)
		}
		reply(w, nil, err)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
	return true
}

func (l *live) nativePermissionState(id string) (*platforms.NativePermissionState, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.syncOwnedLocked()
	o := l.owned[id]
	if o == nil {
		return nil, platforms.ErrNotFound
	}
	if l.foreign(o) {
		return nil, l.foreignError(*o)
	}
	mode := nativeMode(o.NativeMode)
	pending := mode == "auto"
	if p := l.procs[id]; p != nil {
		mode = nativeMode(p.nativeMode)
		p.controlMu.Lock()
		pending = !p.initialized && mode == "auto"
		p.controlMu.Unlock()
	}
	return &platforms.NativePermissionState{Mode: mode, Pending: pending}, nil
}

// Serialize mode changes per live, including persistence after the CLI ack.
// Model/status events still use l.mu and never wait for a control response.
func (l *live) setNativeMode(ctx context.Context, id string, mode *string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if mode == nil || (*mode != "default" && *mode != "auto") {
		return statusError{http.StatusBadRequest, errors.New("native permission mode must be default or auto")}
	}
	l.nativeModeMu.Lock()
	defer l.nativeModeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := l.nativePermissionState(id); err != nil {
		return err
	}
	l.mu.Lock()
	p := l.procs[id]
	if p == nil {
		err := l.updateOwnedLocked(func() { l.owned[id].NativeMode = *mode })
		l.mu.Unlock()
		return err
	}
	l.mu.Unlock()
	if err := l.initializeProc(p); err != nil {
		return err
	}
	if _, err := l.control(ctx, p, "", map[string]any{"subtype": "set_permission_mode", "mode": *mode}); err != nil {
		return statusError{http.StatusUnprocessableEntity, err}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.procs[id] != p {
		return platforms.ErrBusy
	}
	err := l.updateOwnedLocked(func() { l.owned[id].NativeMode = *mode })
	if err != nil {
		_ = p.cmd.Process.Kill()
	} else {
		p.nativeMode = *mode
	}
	return err
}

func (l *live) observeNativeMode(p *proc, raw []byte) {
	var status struct {
		Subtype string `json:"subtype"`
		Mode    string `json:"permissionMode"`
	}
	if json.Unmarshal(raw, &status) != nil || (status.Subtype != "status" && status.Subtype != "init") || status.Mode == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	p.nativeMode = status.Mode
	if err := l.updateOwnedLocked(func() {
		if o := l.owned[p.id]; o != nil {
			o.NativeMode = status.Mode
		}
	}); err != nil {
		_ = p.cmd.Process.Kill()
	}
}
