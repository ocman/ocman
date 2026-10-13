package state

import "testing"

func TestDefaultPlatform(t *testing.T) {
	db, err := Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, value := range []string{"", "fake-provider"} {
		if err := db.SetSetting(t.Context(), DefaultPlatformSetting, value); err != nil {
			t.Fatal(err)
		}
		got, err := db.DefaultPlatform(t.Context())
		want := value
		if want == "" {
			want = "opencode"
		}
		if err != nil || got != want {
			t.Fatalf("default = %q, %v; want %q", got, err, want)
		}
	}
	var absent *DB
	if got, err := absent.DefaultPlatform(t.Context()); err != nil || got != "opencode" {
		t.Fatalf("absent = %q, %v", got, err)
	}
	_ = db.Close()
	if _, err := db.DefaultPlatform(t.Context()); err == nil {
		t.Fatal("closed database must report error")
	}
}

func TestDefaultPlatformIsAlwaysEnabled(t *testing.T) {
	db, err := Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enabled := []string{"claudecode"}
	db.SetEnabledPlatforms(func() []string { return enabled })
	for _, tc := range []struct {
		saved   string
		enabled []string
		want    string
	}{
		{"", []string{"claudecode"}, "claudecode"},                       // Claude-only: never the disabled OpenCode
		{"opencode", []string{"claudecode"}, "claudecode"},               // a stale saved choice falls back
		{"", []string{"opencode", "claudecode"}, "opencode"},             // OpenCode stays the default when installed
		{"claudecode", []string{"opencode", "claudecode"}, "claudecode"}, // the saved choice wins when enabled
		{"gone", []string{"zeta", "beta"}, "beta"},                       // else the lowest enabled id
		{"gone", nil, "gone"},                                            // nothing known: unfiltered
	} {
		enabled = tc.enabled
		if err := db.SetSetting(t.Context(), DefaultPlatformSetting, tc.saved); err != nil {
			t.Fatal(err)
		}
		if got, err := db.DefaultPlatform(t.Context()); err != nil || got != tc.want {
			t.Errorf("saved %q, enabled %v: default = %q, %v; want %q", tc.saved, tc.enabled, got, err, tc.want)
		}
	}
}
