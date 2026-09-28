package state

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestValidateProjectModels(t *testing.T) {
	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = "p/m" + strings.Repeat("x", i)
	}
	cases := []struct {
		name   string
		models []string
		ok     bool
	}{
		{"empty", nil, true},
		{"single", []string{"anthropic/claude-opus"}, true},
		{"nested model id", []string{"openrouter/anthropic/claude"}, true},
		{"ten entries", eleven[:10], true},
		{"eleven entries", eleven, false},
		{"no slash", []string{"claude"}, false},
		{"empty provider", []string{"/claude"}, false},
		{"empty model", []string{"anthropic/"}, false},
		{"inner whitespace", []string{"anthropic/claude opus"}, false},
		{"leading whitespace", []string{" anthropic/claude"}, false},
		{"tab", []string{"anthropic/claude\t"}, false},
		{"300 chars", []string{"p/" + strings.Repeat("m", 298)}, true},
		{"301 chars", []string{"p/" + strings.Repeat("m", 299)}, false},
		{"duplicate", []string{"a/b", "c/d", "a/b"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := ValidateProjectModels(c.models); (err == nil) != c.ok {
				t.Fatalf("ValidateProjectModels(%v) err=%v, want ok=%v", c.models, err, c.ok)
			}
		})
	}
}

func TestProjectSettingKeyFold(t *testing.T) {
	cases := []struct{ dir, want string }{
		{"/src/foo", "project:/src/foo"},
		{"/src/foo/", "project:/src/foo"},
		{"/src/.worktrees/foo/feature-a", "project:/src/foo"},
		{"/src/.worktrees/foo/feature-a/sub", "project:/src/foo"},
		{"/src/bar", "project:/src/bar"},
	}
	for _, c := range cases {
		if got := projectSettingKey(c.dir); got != c.want {
			t.Errorf("projectSettingKey(%q) = %q; want %q", c.dir, got, c.want)
		}
	}
}

func TestProjectSettingsRoundTrip(t *testing.T) {
	d := openTestDB(t)
	ctx := t.Context()

	got, err := d.GetProjectSettings(ctx, "/src/foo")
	if err != nil || got.Models == nil || len(got.Models) != 0 || got.Off {
		t.Fatalf("unconfigured = %+v, %v; want empty", got, err)
	}

	want := ProjectSettings{Models: []string{"a/b", "c/d"}, Off: true}
	if err := d.SetProjectSettings(ctx, "/src/.worktrees/foo/wt", want); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.GetProjectSettings(ctx, "/src/foo"); !reflect.DeepEqual(got, want) {
		t.Fatalf("root read = %+v; want %+v", got, want)
	}

	err = d.SetProjectSettings(ctx, "/src/foo", ProjectSettings{Models: []string{"bad"}})
	if !errors.Is(err, ErrInvalidProjectSettings) {
		t.Fatalf("invalid err = %v", err)
	}
	if got, _ := d.GetProjectSettings(ctx, "/src/foo"); !reflect.DeepEqual(got, want) {
		t.Fatalf("invalid write changed state: %+v", got)
	}

	if err := d.SetProjectSettings(ctx, "/src/foo", ProjectSettings{Off: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := d.GetSetting(ctx, "project:/src/foo"); ok {
		t.Fatal("empty list should delete the row")
	}
}
