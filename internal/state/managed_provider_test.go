package state

import (
	"path/filepath"
	"testing"
	"time"
)

func TestManagedProviderPersistenceAndIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := t.Context()
	if _, ok, err := d.GetManagedProvider(ctx, "a", "/repo"); err != nil || ok {
		t.Fatal(ok, err)
	}
	first := ManagedInstance{Endpoint: "unix:///one", Kind: "provider-tmux", RuntimeID: "a", PID: 1, LaunchedAt: time.Unix(100, 0)}
	second := first
	second.Endpoint = "unix:///two"
	for _, provider := range []string{"a", "b"} {
		if err := d.UpsertManagedProvider(ctx, provider, "/repo", first); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.UpsertManagedProvider(ctx, "a", "/repo", second); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for provider, expected := range map[string]string{"a": second.Endpoint, "b": first.Endpoint} {
		got, ok, err := d.GetManagedProvider(ctx, provider, "/repo")
		if err != nil || !ok || got.Endpoint != expected || !got.LaunchedAt.Equal(first.LaunchedAt) {
			t.Fatal(got, ok, err)
		}
	}
	if err := d.UpsertManagedProvider(ctx, "a", "/other", first); err != nil {
		t.Fatal(err)
	}
	if roots, err := d.ListManagedProviders(ctx, "a"); err != nil || len(roots) != 2 || roots[0] != "/other" || roots[1] != "/repo" {
		t.Fatal(roots, err)
	}
	if err := d.DeleteManagedProvider(ctx, "a", "/repo"); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteManagedProvider(ctx, "a", "/repo"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := d.GetManagedProvider(ctx, "b", "/repo"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.GetManagedProvider(ctx, "a", "/repo"); err == nil {
		t.Fatal("read error lost")
	}
	if err := d.UpsertManagedProvider(ctx, "a", "/repo", first); err == nil {
		t.Fatal("write error lost")
	}
	if err := d.DeleteManagedProvider(ctx, "a", "/repo"); err == nil {
		t.Fatal("delete error lost")
	}
	if _, err := d.ListManagedProviders(ctx, "a"); err == nil {
		t.Fatal("list error lost")
	}
}
