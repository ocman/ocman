package state

import (
	"encoding/json"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagedSettingsRequireManagedMode(t *testing.T) {
	db := openTestStateDB(t)
	if db.ManagedMode() {
		t.Fatal("managed mode on by default")
	}
	if err := db.ApplyManagedSettings(t.Context(), ManagedSettings{Hub: "hub"}, 1); !errors.Is(err, ErrNotManaged) {
		t.Fatalf("apply outside managed mode = %v", err)
	}
	// Unmanaged instances keep writing their own settings.
	if err := db.SetSetting(t.Context(), JudgeModelSettingKey, "a/b"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetJudgeDelayMs(t.Context(), 10); err != nil {
		t.Fatal(err)
	}
	if _, ok := db.ManagedSettings(); ok {
		t.Fatal("snapshot without managed mode")
	}
}

func TestManagedSettingsOverlayLocalValues(t *testing.T) {
	ctx := t.Context()
	db := openTestStateDB(t)
	local := []PromptSection{{Title: "local", Content: "own"}}
	if err := db.SetSetting(ctx, JudgeModelSettingKey, "local/model"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetJudgeDelayMs(ctx, 1111); err != nil {
		t.Fatal(err)
	}
	if err := db.SetPromptSections(ctx, local); err != nil {
		t.Fatal(err)
	}
	if err := db.EnableManagedSettings(ctx); err != nil {
		t.Fatal(err)
	}

	// Before the first snapshot the remote's own values still apply.
	if v, _, _ := db.GetSetting(ctx, JudgeModelSettingKey); v != "local/model" {
		t.Fatalf("pre-snapshot model = %q", v)
	}

	hub := []PromptSection{{Title: "hub", Content: "rules"}}
	if err := db.ApplyManagedSettings(ctx, ManagedSettings{Hub: "hub-1", JudgeModel: "hub/model", JudgeDelayMs: -5, PromptSections: hub}, 42); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := db.GetSetting(ctx, JudgeModelSettingKey); err != nil || !ok || v != "hub/model" {
		t.Fatalf("model = %q %v %v", v, ok, err)
	}
	if ms, err := db.GetJudgeDelayMs(ctx); err != nil || ms != 0 {
		t.Fatalf("delay = %d %v", ms, err)
	}
	got, err := db.GetPromptSections(ctx)
	if err != nil || !reflect.DeepEqual(got, hub) {
		t.Fatalf("sections = %+v %v", got, err)
	}
	got[0].Title = "mutated"
	if again, _ := db.GetPromptSections(ctx); again[0].Title != "hub" {
		t.Fatal("caller mutated the snapshot")
	}
	if snap, ok := db.ManagedSettings(); !ok || snap.Hub != "hub-1" || snap.AppliedAt != 42 {
		t.Fatalf("snapshot = %+v", snap)
	}

	// An empty hub model means "default", not "fall back to the remote's".
	if err := db.ApplyManagedSettings(ctx, ManagedSettings{Hub: "hub-1"}, 43); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := db.GetSetting(ctx, JudgeModelSettingKey); ok || v != "" {
		t.Fatalf("unset hub model = %q %v", v, ok)
	}
	if got, _ := db.GetPromptSections(ctx); got == nil || len(got) != 0 {
		t.Fatalf("unset hub sections = %#v", got)
	}

	// Unmanaged keys are untouched by the layer.
	if err := db.SetSetting(ctx, "other", "x"); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := db.GetSetting(ctx, "other"); !ok || v != "x" {
		t.Fatal("unmanaged key lost")
	}
}

func TestManagedSettingsRejectLocalWrites(t *testing.T) {
	ctx := t.Context()
	db := openTestStateDB(t)
	if err := db.EnableManagedSettings(ctx); err != nil {
		t.Fatal(err)
	}
	for name, write := range map[string]func() error{
		"model":    func() error { return db.SetSetting(ctx, JudgeModelSettingKey, "x/y") },
		"delay":    func() error { return db.SetJudgeDelayMs(ctx, 1) },
		"sections": func() error { return db.SetPromptSections(ctx, nil) },
	} {
		if err := write(); !errors.Is(err, ErrManagedSetting) {
			t.Errorf("%s write = %v", name, err)
		}
	}
}

// The snapshot survives a restart so a disconnected remote keeps the hub's
// settings, and the remote's own rows come back once managed mode is off.
func TestManagedSettingsPersistAndPreserveLocalRows(t *testing.T) {
	ctx := t.Context()
	path := templateDBPath(t)
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetJudgeDelayMs(ctx, 777); err != nil {
		t.Fatal(err)
	}
	if err := db.EnableManagedSettings(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyManagedSettings(ctx, ManagedSettings{Hub: "hub-1", JudgeDelayMs: 50}, 1); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if ms, _ := db.GetJudgeDelayMs(ctx); ms != 777 {
		t.Fatalf("unmanaged restart delay = %d, want the remote's own value", ms)
	}
	if err := db.EnableManagedSettings(ctx); err != nil {
		t.Fatal(err)
	}
	if ms, _ := db.GetJudgeDelayMs(ctx); ms != 50 {
		t.Fatalf("managed restart delay = %d, want the hub's saved value", ms)
	}
}

func TestManagedSettingsCorruptSnapshot(t *testing.T) {
	db := openTestStateDB(t)
	if err := db.setSetting(t.Context(), managedSettingsKey, "{"); err != nil {
		t.Fatal(err)
	}
	if err := db.EnableManagedSettings(t.Context()); err == nil || db.ManagedMode() {
		t.Fatalf("corrupt snapshot accepted: %v", err)
	}
}

func TestManagedSettingsChangeNotification(t *testing.T) {
	ctx := t.Context()
	db := openTestStateDB(t)
	db.notifyManagedSettingsChange() // no callback registered
	calls := 0
	db.OnManagedSettingsChange(func() { calls++ })
	_ = db.SetSetting(ctx, "unrelated", "x")
	if calls != 0 {
		t.Fatal("unrelated key notified")
	}
	_ = db.SetSetting(ctx, JudgeModelSettingKey, "a/b")
	_ = db.SetJudgeDelayMs(ctx, 5)
	_ = db.SetPromptSections(ctx, nil)
	if calls != 3 {
		t.Fatalf("calls = %d", calls)
	}
	if err := db.EnableManagedSettings(ctx); err != nil {
		t.Fatal(err)
	}
	_ = db.SetJudgeDelayMs(ctx, 6)
	if calls != 3 {
		t.Fatal("rejected write notified")
	}
}

func TestHubManagedSettingsSnapshot(t *testing.T) {
	ctx := t.Context()
	db := openTestStateDB(t)
	sections := []PromptSection{{Title: "t", Content: "c"}}
	_ = db.SetSetting(ctx, JudgeModelSettingKey, "p/m")
	_ = db.SetJudgeDelayMs(ctx, 250)
	_ = db.SetPromptSections(ctx, sections)
	got, err := db.HubManagedSettings(ctx, "hub-1", true)
	want := ManagedSettings{Hub: "hub-1", AutoApproveDefault: true, JudgeModel: "p/m", JudgeDelayMs: 250, PromptSections: sections}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %+v %v", got, err)
	}
	_ = db.Close()
	if _, err := db.HubManagedSettings(ctx, "hub-1", false); err == nil {
		t.Fatal("closed database read succeeded")
	}
}

// Regression: forwarding every received snapshot made two managed instances
// that manage each other resend the same settings forever.
func TestManagedSettingsForwardOnlyChanges(t *testing.T) {
	ctx := t.Context()
	db := openTestStateDB(t)
	if err := db.EnableManagedSettings(ctx); err != nil {
		t.Fatal(err)
	}
	calls := 0
	db.OnManagedSettingsChange(func() { calls++ })
	snapshot := ManagedSettings{Hub: "hub-a", JudgeModel: "p/m", JudgeDelayMs: 5, PromptSections: []PromptSection{{Title: "t"}}}
	if err := db.ApplyManagedSettings(ctx, snapshot, 1); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("first snapshot forwarded %d times", calls)
	}
	// Same values from another sender at another time: metadata only.
	snapshot.Hub = "hub-b"
	if err := db.ApplyManagedSettings(ctx, snapshot, 2); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("unchanged values were forwarded")
	}
	if got, _ := db.ManagedSettings(); got.Hub != "hub-b" || got.AppliedAt != 2 {
		t.Fatalf("metadata not stored: %+v", got)
	}
	// nil and empty prompt sections are the same value.
	snapshot.PromptSections = nil
	snapshot.JudgeDelayMs = 6
	if err := db.ApplyManagedSettings(ctx, snapshot, 3); err != nil || calls != 2 {
		t.Fatalf("changed delay forwarded %d times: %v", calls, err)
	}
	snapshot.PromptSections = []PromptSection{}
	if err := db.ApplyManagedSettings(ctx, snapshot, 4); err != nil || calls != 2 {
		t.Fatalf("empty sections after nil forwarded %d times: %v", calls, err)
	}
	snapshot.AutoApproveDefault = true
	if err := db.ApplyManagedSettings(ctx, snapshot, 5); err != nil || calls != 3 {
		t.Fatalf("changed default forwarded %d times: %v", calls, err)
	}
}

// Regression: concurrent hubs could persist one snapshot and publish another,
// so a restart silently switched to a different approval policy.
func TestManagedSettingsPersistedMatchesActive(t *testing.T) {
	ctx := t.Context()
	db := openTestStateDB(t)
	if err := db.EnableManagedSettings(ctx); err != nil {
		t.Fatal(err)
	}
	layer := db.managed.Load()
	var paused atomic.Bool
	second := make(chan error, 1)
	// Hub A pauses after persisting; hub B applies in that window.
	layer.afterPersist = func() {
		if paused.Swap(true) {
			return
		}
		func() {
			go func() { second <- db.ApplyManagedSettings(ctx, ManagedSettings{Hub: "hub-b", JudgeDelayMs: 2}, 2) }()
			select {
			case err := <-second:
				second <- err
			case <-time.After(200 * time.Millisecond): // B waits for A
			}
		}()
	}
	if err := db.ApplyManagedSettings(ctx, ManagedSettings{Hub: "hub-a", JudgeDelayMs: 1}, 1); err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	active, _ := db.ManagedSettings()
	raw, _, err := db.GetSetting(ctx, managedSettingsKey)
	if err != nil {
		t.Fatal(err)
	}
	var persisted ManagedSettings
	if err := json.Unmarshal([]byte(raw), &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Hub != active.Hub {
		t.Fatalf("persisted %s but running sessions use %s", persisted.Hub, active.Hub)
	}
}
