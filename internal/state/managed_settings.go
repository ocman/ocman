package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
)

// JudgeModelSettingKey stores the auto-approve judge model as
// "provider/model". It is a managed setting on a managed remote.
const JudgeModelSettingKey = "judge_model"

// managedSettingsKey persists the hub's last snapshot so a managed remote
// keeps the hub's settings across restarts and while disconnected.
const managedSettingsKey = "remote.managed_settings"

var (
	// ErrManagedSetting rejects a local write to a setting the controlling
	// hub supplies.
	ErrManagedSetting = errors.New("setting is managed by the controlling hub")
	// ErrNotManaged rejects a snapshot sent to an instance not started in
	// managed mode.
	ErrNotManaged = errors.New("instance is not in remote-managed mode")
)

// ManagedSettings is the session settings snapshot a hub sends to a managed
// remote. Every field is the hub's effective value, not an override, so an
// unset hub value replaces the remote's own value with the default.
//
// Keep this an explicit allowlist. Secrets, credentials, installed
// integrations and machine paths belong to the machine and never sync.
type ManagedSettings struct {
	// Hub is the sending hub's instance ID, recorded for display.
	Hub string `json:"hub"`
	// AppliedAt is set by the remote when it stores the snapshot.
	AppliedAt          int64           `json:"appliedAt,omitempty"`
	AutoApproveDefault bool            `json:"autoApproveDefault"`
	JudgeModel         string          `json:"judgeModel"`
	JudgeDelayMs       int64           `json:"judgeDelayMs"`
	PromptSections     []PromptSection `json:"promptSections"`
}

// managedLayer is present only in managed mode. snapshot is nil until the
// first hub snapshot arrives, so the remote's own values apply until then.
type managedLayer struct {
	// applyMu orders persisting and publishing across concurrent hubs.
	applyMu  sync.Mutex
	snapshot atomic.Pointer[ManagedSettings]
	// afterPersist runs between persisting and publishing a snapshot.
	// Test seam for ordering between concurrent hubs; nil in production.
	afterPersist func()
}

// EnableManagedSettings puts the database in remote-managed mode and loads
// the last snapshot a hub sent. Local writes to managed settings fail from
// now on. Call once at startup, before serving.
func (d *DB) EnableManagedSettings(ctx context.Context) error {
	layer := &managedLayer{}
	raw, ok, err := d.GetSetting(ctx, managedSettingsKey)
	if err != nil {
		return err
	}
	if ok {
		var snapshot ManagedSettings
		if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
			return fmt.Errorf("decoding managed settings: %w", err)
		}
		layer.snapshot.Store(&snapshot)
	}
	d.managed.Store(layer)
	return nil
}

// ManagedMode reports whether EnableManagedSettings ran.
func (d *DB) ManagedMode() bool { return d.managed.Load() != nil }

// ManagedSettings returns the active hub snapshot, if any.
func (d *DB) ManagedSettings() (ManagedSettings, bool) {
	if s := d.managedSnapshot(); s != nil {
		return *s, true
	}
	return ManagedSettings{}, false
}

func (d *DB) managedSnapshot() *ManagedSettings {
	if layer := d.managed.Load(); layer != nil {
		return layer.snapshot.Load()
	}
	return nil
}

// ApplyManagedSettings stores a hub snapshot and makes it effective. It
// fails with ErrNotManaged unless EnableManagedSettings ran.
func (d *DB) ApplyManagedSettings(ctx context.Context, snapshot ManagedSettings, now int64) error {
	layer := d.managed.Load()
	if layer == nil {
		return ErrNotManaged
	}
	if snapshot.JudgeDelayMs < 0 {
		snapshot.JudgeDelayMs = 0
	}
	if snapshot.PromptSections == nil {
		snapshot.PromptSections = []PromptSection{}
	}
	snapshot.AppliedAt = now
	changed, err := d.storeManagedSettings(ctx, layer, snapshot)
	if err != nil {
		return err
	}
	// A managed remote that is also a hub forwards what it received, but
	// only real changes: two instances managing each other would otherwise
	// resend the same settings forever.
	if changed {
		d.notifyManagedSettingsChange()
	}
	return nil
}

// storeManagedSettings persists and publishes under one lock, so the
// snapshot running sessions use is always the one a restart loads.
func (d *DB) storeManagedSettings(ctx context.Context, layer *managedLayer, snapshot ManagedSettings) (changed bool, err error) {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return false, err
	}
	layer.applyMu.Lock()
	defer layer.applyMu.Unlock()
	if err := d.setSetting(ctx, managedSettingsKey, string(raw)); err != nil {
		return false, err
	}
	if layer.afterPersist != nil {
		layer.afterPersist()
	}
	previous := layer.snapshot.Swap(&snapshot)
	return previous == nil || !sameManagedValues(*previous, snapshot), nil
}

// sameManagedValues compares effective settings, ignoring sender metadata.
func sameManagedValues(a, b ManagedSettings) bool {
	a.Hub, a.AppliedAt, b.Hub, b.AppliedAt = "", 0, "", 0
	return reflect.DeepEqual(a, b)
}

// managedSettingValue answers GetSetting for a key the hub supplies.
func (d *DB) managedSettingValue(key string) (value string, ok, managed bool) {
	s := d.managedSnapshot()
	if s == nil {
		return "", false, false
	}
	switch key {
	case JudgeModelSettingKey:
		return s.JudgeModel, s.JudgeModel != "", true
	}
	return "", false, false
}

// rejectManagedWrite fails a local write to a managed setting. Writes are
// refused in managed mode even before the first snapshot, so a local edit
// cannot silently vanish when the hub's snapshot arrives.
func (d *DB) rejectManagedWrite(key string) error {
	if d.managed.Load() == nil {
		return nil
	}
	switch key {
	case JudgeModelSettingKey, managedJudgeDelayKey, managedPromptSectionsKey:
		return fmt.Errorf("%w: %s", ErrManagedSetting, key)
	}
	return nil
}

// Pseudo-keys for managed settings stored outside the setting table.
const (
	managedJudgeDelayKey     = "judge_delay"
	managedPromptSectionsKey = "prompt_sections"
)

// OnManagedSettingsChange registers fn to run after a local write changes a
// setting that hubs send to managed remotes. Only one callback is kept.
func (d *DB) OnManagedSettingsChange(fn func()) {
	d.managedChanged.Store(&fn)
}

func (d *DB) notifyManagedSettingsChange() {
	if fn := d.managedChanged.Load(); fn != nil && *fn != nil {
		(*fn)()
	}
}

// HubManagedSettings builds the snapshot this instance sends to the remotes
// it manages, from its own effective settings.
func (d *DB) HubManagedSettings(ctx context.Context, hub string, autoApproveDefault bool) (ManagedSettings, error) {
	model, _, err := d.GetSetting(ctx, JudgeModelSettingKey)
	if err != nil {
		return ManagedSettings{}, err
	}
	delay, err := d.GetJudgeDelayMs(ctx)
	if err != nil {
		return ManagedSettings{}, err
	}
	sections, err := d.GetPromptSections(ctx)
	if err != nil {
		return ManagedSettings{}, err
	}
	return ManagedSettings{Hub: hub, AutoApproveDefault: autoApproveDefault, JudgeModel: model, JudgeDelayMs: delay, PromptSections: sections}, nil
}
