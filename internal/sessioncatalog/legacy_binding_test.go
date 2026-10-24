package sessioncatalog

import (
	"errors"
	"testing"
)

func TestFindLegacyBindingRequiresOneBindingAcrossSources(t *testing.T) {
	s, _ := openStore(t)
	b := binding()
	if _, err := s.FindLegacyBinding(t.Context(), b.OwnerID, b.Platform, b.NativeID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("absent binding: %v", err)
	}
	row, _, err := s.Reserve(t.Context(), reservation())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Bind(t.Context(), row.ID, b); err != nil {
		t.Fatal(err)
	}
	if got, err := s.FindLegacyBinding(t.Context(), b.OwnerID, b.Platform, b.NativeID); err != nil || got.ID != row.ID {
		t.Fatalf("single binding: %+v %v", got, err)
	}
	r := reservation()
	r.RequestKey = "replacement-source"
	other, _, err := s.Reserve(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	b.SourceKey = "source-2"
	if _, err := s.Bind(t.Context(), other.ID, b); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FindLegacyBinding(t.Context(), b.OwnerID, b.Platform, b.NativeID); !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("ambiguous binding: %v", err)
	}
	for _, args := range [][3]string{{"", b.Platform, b.NativeID}, {b.OwnerID, " ", b.NativeID}, {b.OwnerID, b.Platform, ""}, {b.OwnerID, b.Platform, row.ID}} {
		if _, err := s.FindLegacyBinding(t.Context(), args[0], args[1], args[2]); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid %v: %v", args, err)
		}
	}
}

func TestRetainLegacyKeysIsOwnerScoped(t *testing.T) {
	s, _ := openStore(t)
	row, _, err := s.Reserve(t.Context(), reservation())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RetainLegacyKeys(t.Context(), "other-owner", row.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner retain: %v", err)
	}
	if err := s.RetainLegacyKeys(t.Context(), row.OwnerID, "native-1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("native id retain: %v", err)
	}
	if err := s.RetainLegacyKeys(t.Context(), row.OwnerID, row.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(t.Context(), row.ID); err != nil || !got.LegacyKeys {
		t.Fatalf("legacy keys not retained: %+v %v", got, err)
	}
}

func TestFilePathNamesTheCatalogueDatabase(t *testing.T) {
	s, _ := openStore(t)
	if path, err := s.FilePath(t.Context()); err != nil || path == "" {
		t.Fatalf("file path: %q %v", path, err)
	}
}
