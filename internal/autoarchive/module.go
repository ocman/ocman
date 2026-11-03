// Package autoarchive owns the auto-archive settings route and the joined
// daily worker that archives inactive sessions and projects. Archive rows,
// snooze deadlines and unarchive markers remain in internal/state, shared
// with manual archive and snooze.
package autoarchive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

const (
	interval          = 24 * time.Hour
	settingKey        = "auto_archive"
	defaultTTLDays    = 7
	maximumTTLDays    = 3650
	passTimeout       = time.Minute
	defaultRPCTimeout = 10 * time.Second
)

type Deps struct {
	// Store is nil without a state database; the worker then idles and
	// the settings route answers 503.
	Store *state.DB
	// Platforms lists the registered adapters at call time.
	Platforms func() []platforms.Platform
	// Local is this machine's host, whose projects the project pass archives.
	Local func() hostsvc.Host
	// StoredHistory reports whether project activity is persisted, which
	// the project pass needs.
	StoredHistory func() bool
}

// Settings is the stored auto-archive configuration.
type Settings struct {
	Enabled bool `json:"enabled"`
	TTLDays int  `json:"ttlDays"`
}

type Module struct {
	d Deps
	// platformTimeout bounds one platform's inactive-session read so a
	// slow remote cannot starve local maintenance.
	platformTimeout time.Duration
}

var _ module.Module = (*Module)(nil)
var _ module.Runner = (*Module)(nil)

func NewModule(d Deps) *Module { return &Module{d: d, platformTimeout: defaultRPCTimeout} }
func (*Module) Name() string   { return "autoarchive" }

func (m *Module) Routes(r module.Router) {
	m.setting().Mount(r.Setting, "/api/settings/auto-archive")
}

// Run archives inactive sessions and projects on startup and then daily.
func (m *Module) Run(ctx context.Context) {
	if m.d.Store == nil {
		return
	}
	module.Every(ctx, "auto-archive", interval, func(ctx context.Context) {
		m.ArchiveSessions(ctx)
		m.ArchiveProjects(ctx)
	})
}

func (m *Module) setting() httpx.SettingRoute[Settings] {
	return httpx.SettingRoute[Settings]{
		Name:      "auto-archive settings",
		Available: func() bool { return m.d.Store != nil },
		Get:       func(ctx context.Context) (any, error) { return m.Settings(ctx) },
		Validate:  func(v *Settings) error { return validate(*v) },
		Set:       m.SetSettings,
	}
}

// Settings returns the stored settings, defaulting to enabled after seven
// days of inactivity.
func (m *Module) Settings(ctx context.Context) (Settings, error) {
	settings := Settings{Enabled: true, TTLDays: defaultTTLDays}
	if m.d.Store == nil {
		return settings, errors.New("state database not available")
	}
	value, ok, err := m.d.Store.GetSetting(ctx, settingKey)
	if err != nil || !ok {
		return settings, err
	}
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return settings, fmt.Errorf("decode auto-archive settings: %w", err)
	}
	if err := validate(settings); err != nil {
		return settings, err
	}
	return settings, nil
}

// SetSettings validates and stores settings.
func (m *Module) SetSettings(ctx context.Context, settings Settings) error {
	if err := validate(settings); err != nil {
		return err
	}
	value, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	return m.d.Store.SetSetting(ctx, settingKey, string(value))
}

func validate(settings Settings) error {
	if settings.TTLDays < 1 || settings.TTLDays > maximumTTLDays {
		return fmt.Errorf("ttlDays must be between 1 and %d", maximumTTLDays)
	}
	return nil
}
