package configfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestDocumentValidation(t *testing.T) {
	for _, input := range []string{
		"", "[]", "version: 2", "version: '1'", "version: 1\nunknown: true",
		"version: 1\nsettings: []", "version: 1\nstartup: []",
		"version: 1\nsettings: {a: 1, a: 2}", "version: 1\n---\nversion: 1",
		"version: 1\nsettings: {a: &x 1, b: *x}", "version: 1\nsettings: {1: x}",
		"version: 1\nsettings: {a: !secret x}", strings.Repeat("x", maxSize+1),
	} {
		if _, err := Parse([]byte(input)); err == nil {
			t.Errorf("accepted invalid document: %.80s", input)
		}
	}
}

func TestFileEditsPreserveCommentsAndExternalChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	initial := "# header\nversion: 1\nstartup:\n  addr: localhost:8228 # listen\nsettings:\n  # preferred\n  judge_model: old/model # keep\n  flags:\n    enabled: false # nested\n"
	if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	f := &File{Path: path}
	err := f.Update(func(d *Document) error {
		if err := d.Set("settings", "flags", map[string]bool{"enabled": false}); err != nil {
			return err
		}
		return d.Set("settings", "judge_model", "new/model")
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, part := range []string{"# header", "# preferred", "# keep", "# listen", "# nested", "new/model", "enabled: false"} {
		if !strings.Contains(string(data), part) {
			t.Errorf("lost %s in %s", part, data)
		}
	}
	doc, err := f.Read()
	if err != nil {
		t.Fatal(err)
	}
	value, ok, err := doc.Get("settings", "flags")
	if err != nil || !ok || value != `{"enabled":false}` {
		t.Fatalf("structured value = %q, %v, %v", value, ok, err)
	}
	if _, ok, err := doc.Get("settings", "absent"); err != nil || ok {
		t.Fatal("missing value is present")
	}
	err = f.Update(func(d *Document) error {
		if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
			return err
		}
		return d.Set("settings", "judge_model", "lost/model")
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != initial {
		t.Fatal("overwrote external edit")
	}
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	if err := f.Update(func(d *Document) error { return d.Set("settings", "judge_model", "no") }); err == nil {
		t.Fatal("edited read-only file")
	}
}

func TestCreateNeverClobbersAndSecuresFile(t *testing.T) {
	f := &File{Path: filepath.Join(t.TempDir(), "new", "config.yaml")}
	d, err := Parse([]byte("version: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Set("settings", "a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := f.Create(d); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(f.Path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("permissions = %o", info.Mode().Perm())
	}
	if err := f.Create(d); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second creation = %v", err)
	}
}

func TestFileRefusesUnsafeWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	initial := []byte("version: 1\nsettings: {}\n")
	if err := os.WriteFile(path, initial, 0600); err != nil {
		t.Fatal(err)
	}
	f := &File{Path: path}
	want := errors.New("validation refused")
	if err := f.Update(func(*Document) error { return want }); !errors.Is(err, want) {
		t.Fatalf("callback error = %v", err)
	}
	lock, err := os.OpenFile(path+".lock", os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	if err := f.Update(func(*Document) error { t.Fatal("ran callback without lock"); return nil }); !errors.Is(err, ErrConflict) {
		t.Fatalf("concurrent writer accepted: %v", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "linked.yaml")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	linked := &File{Path: link}
	if _, err := linked.Read(); err != nil {
		t.Fatal("could not read config symlink")
	}
	if err := linked.Update(func(d *Document) error { return d.Set("settings", "a", "b") }); err == nil {
		t.Fatal("replaced config symlink")
	}
	data, _ := os.ReadFile(path)
	if string(data) != string(initial) {
		t.Fatal("failed save modified target")
	}
	if _, err := (&File{Path: filepath.Dir(path)}).Read(); err == nil {
		t.Fatal("accepted directory as config")
	}
	if err := os.WriteFile(path, []byte("version: ["), 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.Update(func(*Document) error { t.Fatal("edited invalid YAML"); return nil }); err == nil {
		t.Fatal("accepted malformed YAML")
	}
}

func TestDocumentValueShapes(t *testing.T) {
	d, err := Parse([]byte("version: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if keys := d.Keys("settings"); len(keys) != 0 {
		t.Fatal("unexpected settings")
	}
	d.Delete("settings", "absent")
	if err := d.Set("settings", "null", nil); err != nil {
		t.Fatal(err)
	}
	if value, ok, err := d.Get("settings", "null"); err != nil || !ok || value != "null" {
		t.Fatalf("null value = %q, %v", value, err)
	}
	if err := d.Set("settings", "bad", invalidYAMLValue{}); err == nil {
		t.Fatal("encoded unsupported value")
	}
}

type invalidYAMLValue struct{}

func (invalidYAMLValue) MarshalYAML() (any, error) {
	return nil, errors.New("cannot encode value")
}
