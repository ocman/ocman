package mcpauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

type memStore struct {
	values  map[string]string
	getErr  error
	setErr  error
	setRuns int
}

func (m *memStore) GetSecretSetting(_ context.Context, key string) (string, bool, error) {
	v, ok := m.values[key]
	return v, ok, m.getErr
}

func (m *memStore) SetSecretSetting(_ context.Context, key, value string) error {
	m.setRuns++
	if m.setErr != nil {
		return m.setErr
	}
	m.values[key] = value
	return nil
}

func TestEnsureGeneratesOncePersistsAndPublishes0600(t *testing.T) {
	store := &memStore{values: map[string]string{}}
	path := filepath.Join(t.TempDir(), "state", FileName)
	token, err := Ensure(t.Context(), store, path)
	if err != nil || len(token) != 64 {
		t.Fatalf("Ensure = %q, %v; want a 64-char token", token, err)
	}
	if store.values[SettingKey] != token {
		t.Fatal("token not persisted in the secret setting")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode = %v, %v; want 0600", info, err)
	}
	if got := ReadToken(path); got != token {
		t.Fatalf("ReadToken = %q, want the token", got)
	}

	// A loosened or deleted file is rewritten with the same token.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	again, err := Ensure(t.Context(), store, path)
	if err != nil || again != token || store.setRuns != 1 {
		t.Fatalf("second Ensure = %q, %v (sets %d); want the stored token", again, err, store.setRuns)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode after repair = %v", info.Mode().Perm())
	}

	// Without a path the token stays in the store only.
	if got, err := Ensure(t.Context(), store, ""); err != nil || got != token {
		t.Fatalf("Ensure without file = %q, %v", got, err)
	}
}

func TestEnsureFailures(t *testing.T) {
	if _, err := Ensure(t.Context(), nil, ""); err == nil {
		t.Fatal("Ensure without a store succeeded")
	}
	boom := errors.New("boom")
	if _, err := Ensure(t.Context(), &memStore{values: map[string]string{}, getErr: boom}, ""); !errors.Is(err, boom) {
		t.Fatalf("get error = %v", err)
	}
	if _, err := Ensure(t.Context(), &memStore{values: map[string]string{}, setErr: boom}, ""); !errors.Is(err, boom) {
		t.Fatalf("set error = %v", err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(t.Context(), &memStore{values: map[string]string{}}, filepath.Join(file, FileName)); err == nil {
		t.Fatal("Ensure wrote under a regular file")
	}
}

func TestReadTokenMissing(t *testing.T) {
	if ReadToken("") != "" || ReadToken(filepath.Join(t.TempDir(), "missing")) != "" {
		t.Fatal("ReadToken invented a token")
	}
}

func TestAuthorized(t *testing.T) {
	const token = "abc123"
	for header, want := range map[string]bool{
		"Bearer abc123":   true,
		"bearer abc123":   true,
		"Bearer  abc123 ": true,
		"Bearer abc1234":  false,
		"Bearer":          false,
		"Basic abc123":    false,
		"":                false,
		"abc123":          false,
	} {
		r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		if got := Authorized(r, token); got != want {
			t.Errorf("Authorized(%q) = %v, want %v", header, got, want)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	SetHeader(r, token)
	if !Authorized(r, token) || Authorized(r, "") {
		t.Fatal("SetHeader did not authorize, or an empty token did")
	}
	if Headers(token)["Authorization"] != "Bearer abc123" || TokenPath("/d") != filepath.Join("/d", FileName) {
		t.Fatal("unexpected header map or token path")
	}
}
