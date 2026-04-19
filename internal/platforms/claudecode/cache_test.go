package claudecode

import "testing"

func TestCache_GetMiss(t *testing.T) {
	c := newCache()
	if got := c.getByMtime("nope", 1, 1); got != nil {
		t.Errorf("expected nil for missing entry, got %+v", got)
	}
}

func TestCache_PutThenGet(t *testing.T) {
	c := newCache()
	pf := &parsedFile{SessionID: "s1"}
	c.putByMtime("/a/b", 1234, 567, pf)
	got := c.getByMtime("/a/b", 1234, 567)
	if got != pf {
		t.Errorf("expected cached entry, got %+v", got)
	}
}

func TestCache_StaleMtime(t *testing.T) {
	c := newCache()
	c.putByMtime("/a/b", 1234, 567, &parsedFile{SessionID: "s1"})
	// Same size but newer mtime: should miss.
	if got := c.getByMtime("/a/b", 9999, 567); got != nil {
		t.Errorf("stale mtime should miss, got %+v", got)
	}
}

func TestCache_StaleSize(t *testing.T) {
	c := newCache()
	c.putByMtime("/a/b", 1234, 567, &parsedFile{SessionID: "s1"})
	if got := c.getByMtime("/a/b", 1234, 9999); got != nil {
		t.Errorf("stale size should miss, got %+v", got)
	}
}

func TestCache_PutOverwrites(t *testing.T) {
	c := newCache()
	c.putByMtime("/a/b", 1, 1, &parsedFile{SessionID: "first"})
	c.putByMtime("/a/b", 2, 2, &parsedFile{SessionID: "second"})
	got := c.getByMtime("/a/b", 2, 2)
	if got == nil || got.SessionID != "second" {
		t.Errorf("expected overwrite, got %+v", got)
	}
	// First entry is gone (same path).
	if c.len() != 1 {
		t.Errorf("expected 1 entry after overwrite, got %d", c.len())
	}
}

func TestCache_Forget(t *testing.T) {
	c := newCache()
	c.putByMtime("/a", 1, 1, &parsedFile{SessionID: "x"})
	c.forget("/a")
	if got := c.getByMtime("/a", 1, 1); got != nil {
		t.Errorf("expected nil after forget, got %+v", got)
	}
}
