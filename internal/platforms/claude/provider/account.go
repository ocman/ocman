package claudeprovider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

// The account operation (AD-2, FR-7) only asks the user's own claude CLI:
// ocman never reads, stores or proxies Claude credentials (requirements
// Overview). The subscription type comes from the control protocol's
// initialize response (spike §5). Utilisation comes only from the structured
// rate_limit_event lines of sessions ocman drives, shared by every supervisor
// through UsagePath; a window the CLI has not reported is simply absent
// (approved FR-7 departure, claude-code-provider.1.3.68).

const (
	accountTTL    = 10 * time.Minute
	initRequestID = "ocman-initialize"
)

// usageWindows maps the CLI's rateLimitType to the protocol's window name.
var usageWindows = map[string]string{"five_hour": "five_hour", "seven_day": "weekly"}

type account struct {
	cli       string
	env       func() []string
	usagePath string
	config    string     // the active Claude config directory: windows are scoped to it
	slots     string     // Options.SlotDir: the probe takes a helper slot
	mu        sync.Mutex // held across the probe: one CLI call at a time
	at        time.Time
	plan      string
	email     string
}

// usageScope is one Claude configuration's observed windows, and the
// account they were observed for once the CLI reported it.
type usageScope struct {
	Email   string                                `json:"email,omitempty"`
	Windows map[string]providerprotocol.RateLimit `json:"windows"`
}

func newAccount(opts Options) *account {
	return &account{cli: opts.CLI, env: opts.childEnv, usagePath: opts.UsagePath, config: opts.ConfigDir, slots: opts.SlotDir}
}

// get returns the subscription type and every unexpired observed window.
// No account in the CLI's initialize response is platforms.ErrNotFound.
func (a *account) get(ctx context.Context) (providerprotocol.Account, error) {
	plan, err := a.subscription(ctx)
	if err != nil {
		return providerprotocol.Account{}, err
	}
	a.mu.Lock()
	email := a.email
	a.mu.Unlock()
	// Windows observed for another login of this configuration are dropped.
	windows := a.update(func(s *usageScope) {
		if email != "" && s.Email != email {
			if s.Email != "" {
				s.Windows = map[string]providerprotocol.RateLimit{}
			}
			s.Email = email
		}
	})
	limits := []providerprotocol.RateLimit{}
	now := time.Now().UnixMilli()
	for _, name := range []string{"five_hour", "weekly"} {
		if w, ok := windows[name]; ok && w.ResetsAt > now {
			limits = append(limits, w)
		}
	}
	return providerprotocol.Account{SubscriptionType: plan, RateLimits: limits}, nil
}

func (a *account) subscription(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.at.IsZero() && time.Since(a.at) < accountTTL {
		return a.plan, nil
	}
	res, err := initialize(ctx, a.slots, a.cli, os.TempDir(), a.env()) // no project settings or hooks
	if err != nil {
		return "", err
	}
	info := res.Account
	if info == nil || (info.SubscriptionType == "" && info.Email == "") {
		return "", platforms.ErrNotFound
	}
	a.plan, a.email, a.at = info.SubscriptionType, info.Email, time.Now()
	return a.plan, nil
}

type initAccount struct {
	Email            string `json:"email"`
	SubscriptionType string `json:"subscriptionType"`
}

// initResponse is the part of the CLI's initialize response ocman reads.
type initResponse struct {
	Account  *initAccount  `json:"account"`
	Commands []initCommand `json:"commands"`
	Agents   []initAgent   `json:"agents"`
	Models   []initModel   `json:"models"`
}

// initTimeout bounds one control-handshake CLI call: a claude that never
// answers initialize is killed, not waited on. Tests shorten it to exercise
// the timeout path without paying 15 s.
var initTimeout atomic.Int64

func init() { initTimeout.Store(int64(15 * time.Second)) }

// initialize runs the CLI in dir only for the control handshake: no user
// message is sent, so no turn runs, no transcript is written and nothing is
// billed. args are extra launch flags (e.g. --plugin-dir).
func initialize(ctx context.Context, slots, cli, dir string, env []string, args ...string) (initResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(initTimeout.Load()))
	defer cancel()
	release, err := HelperSlot(ctx, slots)
	if err != nil {
		return initResponse{}, err
	}
	defer release()
	cmd := exec.CommandContext(ctx, cli, append([]string{"-p", "--verbose", "--input-format", "stream-json", "--output-format", "stream-json"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(env, "PWD="+dir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return initResponse{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return initResponse{}, err
	}
	if err := cmd.Start(); err != nil {
		return initResponse{}, fmt.Errorf("starting claude: %w", err)
	}
	defer func() { _ = stdin.Close(); _ = cmd.Wait() }()
	req, _ := json.Marshal(map[string]any{"type": "control_request", "request_id": initRequestID,
		"request": sdkInitializeRequest()})
	if _, err := stdin.Write(append(req, '\n')); err != nil {
		return initResponse{}, err
	}
	lines := bufio.NewScanner(stdout)
	lines.Buffer(nil, 32<<20)
	for lines.Scan() {
		var m struct {
			Type     string `json:"type"`
			Response struct {
				Subtype   string       `json:"subtype"`
				RequestID string       `json:"request_id"`
				Error     string       `json:"error"`
				Response  initResponse `json:"response"`
			} `json:"response"`
		}
		if json.Unmarshal(lines.Bytes(), &m) != nil || m.Type != "control_response" || m.Response.RequestID != initRequestID {
			continue
		}
		if m.Response.Subtype != "success" {
			return initResponse{}, fmt.Errorf("claude initialize: %s", m.Response.Error)
		}
		return m.Response.Response, nil
	}
	return initResponse{}, errors.New("claude exited before answering initialize")
}

// update applies change to this configuration's windows under the
// cross-process lock and returns them. Windows are keyed by the Claude
// config directory, so another configuration (another account) never shows
// them. Without the lock nothing is written.
func (a *account) update(change func(*usageScope)) map[string]providerprotocol.RateLimit {
	if a.usagePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(a.usagePath), 0o700); err != nil {
		return nil
	}
	lock, err := os.OpenFile(a.usagePath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX) != nil {
		return nil
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	all := map[string]*usageScope{}
	if data, err := os.ReadFile(a.usagePath); err == nil {
		_ = json.Unmarshal(data, &all) // an older, unscoped file starts empty
	}
	scope := all[a.config]
	if scope == nil {
		scope = &usageScope{}
		all[a.config] = scope
	}
	if scope.Windows == nil {
		scope.Windows = map[string]providerprotocol.RateLimit{}
	}
	before, _ := json.Marshal(scope)
	change(scope)
	if after, _ := json.Marshal(scope); string(after) != string(before) {
		data, _ := json.Marshal(all)
		tmp := a.usagePath + ".tmp"
		if os.WriteFile(tmp, data, 0o600) == nil {
			_ = os.Rename(tmp, a.usagePath)
		}
	}
	return scope.Windows
}

// observe records one rate_limit_event (spike §5: utilization is a
// fraction, resetsAt Unix seconds). Events without a utilisation are skipped,
// except a rejection, which means the window is used up.
func (a *account) observe(raw json.RawMessage) {
	var info struct {
		Status      string   `json:"status"`
		ResetsAt    int64    `json:"resetsAt"`
		Type        string   `json:"rateLimitType"`
		Utilization *float64 `json:"utilization"`
	}
	name, ok := "", false
	if json.Unmarshal(raw, &info) == nil {
		name, ok = usageWindows[info.Type]
	}
	if !ok || info.ResetsAt <= 0 {
		return
	}
	used := 1.0
	if info.Utilization != nil {
		used = max(0, min(1, *info.Utilization))
	} else if info.Status != "rejected" {
		return
	}
	a.update(func(s *usageScope) {
		s.Windows[name] = providerprotocol.RateLimit{Name: name, Utilization: used, ResetsAt: info.ResetsAt * 1000}
	})
}
