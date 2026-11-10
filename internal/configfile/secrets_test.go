package configfile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestSecretReferences(t *testing.T) {
	for _, tc := range []struct{ input, provider, reference string }{
		{"secret://env/ENV_NAME", "env", "ENV_NAME"},
		{"secret://file/run/secrets/key", "file", "/run/secrets/key"},
		{"secret://1password/vault/item/field", "1password", "vault/item/field"},
		{"secret://custom/a%23b%3Ac%2520", "custom", "a#b:c%20"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			ref, err := ParseSecret(tc.input)
			if err != nil || ref.Provider != tc.provider || ref.Reference != tc.reference {
				t.Fatalf("unexpected reference: %+v, %v", ref, err)
			}
		})
	}
	for _, input := range []string{"plaintext", "secret://env", "secret://env/", "secret://env/BAD-NAME", "secret://file/a#b", "secret://file/a?b", "secret://env/%00", "secret://x/%zz", "secret://ENV/name"} {
		if _, err := ParseSecret(input); err == nil {
			t.Errorf("accepted malformed reference %q", input)
		}
	}
}

func TestSecretResolver(t *testing.T) {
	t.Setenv("CONFIGFILE_TEST_SECRET", "secret://env/NOT_RECURSIVE")
	r := NewResolver(func(_ context.Context, id string) (string, error) { return "stored:" + id, nil })
	value, err := r.Resolve(t.Context(), "secret://env/CONFIGFILE_TEST_SECRET")
	if err != nil || value != "secret://env/NOT_RECURSIVE" {
		t.Fatalf("env result = %q, %v", value, err)
	}
	value, err = r.Resolve(t.Context(), "secret://ocman/key")
	if err != nil || value != "stored:key" {
		t.Fatalf("ocman result = %q, %v", value, err)
	}
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte(" leading space \r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	value, err = r.Resolve(t.Context(), "secret://file"+path)
	if err != nil || value != " leading space " {
		t.Fatalf("file result = %q, %v", value, err)
	}
	r.Loaders["broken"] = func(context.Context, string) (string, error) { return "", errors.New("sensitive-value") }
	for _, ref := range []string{"secret://unknown/key", "secret://broken/key", "secret://env/CONFIGFILE_TEST_MISSING", "secret://file/not-present", "secret://file" + filepath.Dir(path)} {
		if _, err := r.Resolve(t.Context(), ref); err == nil || strings.Contains(err.Error(), "sensitive-value") {
			t.Fatalf("unsafe/missing error for %s: %v", ref, err)
		}
	}
	t.Setenv("CONFIGFILE_TEST_SECRET", "")
	if _, err := r.Resolve(t.Context(), "secret://env/CONFIGFILE_TEST_SECRET"); err == nil {
		t.Fatal("accepted empty secret")
	}
	if err := os.WriteFile(path, make([]byte, (1<<20)+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(t.Context(), "secret://file"+path); err == nil {
		t.Fatal("accepted oversized file")
	}
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(t.Context(), "secret://file"+fifo); err == nil {
		t.Fatal("accepted FIFO secret")
	}
}
