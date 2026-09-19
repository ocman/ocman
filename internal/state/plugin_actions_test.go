package state

import (
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/plugins"
)

func TestPluginActionAuthorization(t *testing.T) {
	d, desc, _ := pluginFixture(t)
	called := false
	use := func(p plugins.Description, grants []string) error {
		called = true
		if p.ID != desc.ID {
			t.Fatal("wrong identity")
		}
		if len(grants) != 0 {
			return &plugins.WireError{Category: plugins.ErrorPermissionDenied}
		}
		return nil
	}
	for _, id := range []string{desc.ID, "org.example.missing"} {
		if err := d.WithPluginAuthorization(t.Context(), id, use); !errors.Is(err, plugins.ErrUnavailable) {
			t.Fatal(err)
		}
	}
	if called {
		t.Fatal("unauthorized callback")
	}
	requirePluginOK(t, d.SetPluginEnabled(t.Context(), desc.ID, true, []string{"context.owner"}))
	if err := d.WithPluginAuthorization(t.Context(), desc.ID, use); err == nil {
		t.Fatal("lost callback error")
	}
	requirePluginOK(t, d.SetPluginGrants(t.Context(), desc.ID, nil))
	requirePluginOK(t, d.WithPluginAuthorization(t.Context(), desc.ID, use))
	for _, column := range []string{"description_json", "grants_json"} {
		_, err := d.db.Exec(`UPDATE plugin_registration SET `+column+`='{' WHERE id=?`, desc.ID)
		requirePluginOK(t, err)
		if err := d.WithPluginAuthorization(t.Context(), desc.ID, use); !errors.Is(err, ErrPluginState) {
			t.Fatal(err)
		}
	}
	requirePluginOK(t, d.Close())
	if err := d.WithPluginAuthorization(t.Context(), desc.ID, use); !errors.Is(err, ErrPluginState) {
		t.Fatal(err)
	}
}
