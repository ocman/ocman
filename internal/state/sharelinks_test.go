package state

import (
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestCreateAndGetShareLink(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	link, err := db.CreateShareLink("opencode", "ses_123", 0)
	if err != nil {
		t.Fatalf("CreateShareLink: %v", err)
	}
	if link.Token == "" {
		t.Fatal("expected non-empty token")
	}
	if len(link.Token) < 40 {
		t.Errorf("token too short to be unguessable: %q (%d chars)", link.Token, len(link.Token))
	}
	if link.Platform != "opencode" || link.SessionID != "ses_123" {
		t.Errorf("unexpected link fields: %+v", link)
	}
	if link.CreatedAt == 0 {
		t.Error("expected createdAt to be set")
	}

	got, ok, err := db.GetActiveShareLink(link.Token)
	if err != nil {
		t.Fatalf("GetActiveShareLink: %v", err)
	}
	if !ok {
		t.Fatal("expected freshly created link to be active")
	}
	if got.SessionID != "ses_123" || got.Platform != "opencode" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestSetShareRelay(t *testing.T) {
	db := openTestDB(t)
	link, err := db.CreateShareLink("opencode", "ses_relay", 0)
	if err != nil {
		t.Fatalf("CreateShareLink: %v", err)
	}
	if err := db.SetShareRelay(link.Token, "https://share.example.com", "20260813-abc", "key", "delete"); err != nil {
		t.Fatalf("SetShareRelay: %v", err)
	}
	if err := db.SetShareRelaySeq(link.Token, 3); err != nil {
		t.Fatalf("SetShareRelaySeq: %v", err)
	}
	got, ok, err := db.GetActiveShareLink(link.Token)
	if err != nil || !ok {
		t.Fatalf("GetActiveShareLink: ok=%v err=%v", ok, err)
	}
	if got.RelayURL != "https://share.example.com" || got.RelayID != "20260813-abc" || got.RelayKey != "key" || got.RelayDeleteToken != "delete" || got.RelayLastSeq != 3 {
		t.Fatalf("relay fields = %+v", got)
	}
}

func TestShareTokensAreUnique(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		l, err := db.CreateShareLink("opencode", "ses_x", 0)
		if err != nil {
			t.Fatalf("CreateShareLink: %v", err)
		}
		if seen[l.Token] {
			t.Fatalf("duplicate token generated: %q", l.Token)
		}
		seen[l.Token] = true
	}
}

func TestGetActiveShareLinkUnknownToken(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	_, ok, err := db.GetActiveShareLink("does-not-exist")
	if err != nil {
		t.Fatalf("GetActiveShareLink: %v", err)
	}
	if ok {
		t.Fatal("expected unknown token to be inactive")
	}
}

func TestRevokeShareLink(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	link, err := db.CreateShareLink("opencode", "ses_abc", 0)
	if err != nil {
		t.Fatalf("CreateShareLink: %v", err)
	}

	revoked, err := db.RevokeShareLink("opencode", "ses_abc", link.Token)
	if err != nil {
		t.Fatalf("RevokeShareLink: %v", err)
	}
	if !revoked {
		t.Fatal("expected revoke to affect a row")
	}

	if _, ok, _ := db.GetActiveShareLink(link.Token); ok {
		t.Fatal("expected revoked link to be inactive")
	}

	// Revoking again is a no-op.
	revoked, err = db.RevokeShareLink("opencode", "ses_abc", link.Token)
	if err != nil {
		t.Fatalf("RevokeShareLink (second): %v", err)
	}
	if revoked {
		t.Fatal("expected second revoke to affect no rows")
	}
}

func TestRevokeShareLinkScopedToSession(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	link, err := db.CreateShareLink("opencode", "ses_owner", 0)
	if err != nil {
		t.Fatalf("CreateShareLink: %v", err)
	}

	// Wrong session must not be able to revoke it.
	revoked, err := db.RevokeShareLink("opencode", "ses_other", link.Token)
	if err != nil {
		t.Fatalf("RevokeShareLink: %v", err)
	}
	if revoked {
		t.Fatal("expected revoke with mismatched session to affect no rows")
	}
	if _, ok, _ := db.GetActiveShareLink(link.Token); !ok {
		t.Fatal("link should still be active after a mismatched-session revoke attempt")
	}
}

func TestListActiveShareLinks(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	l1, _ := db.CreateShareLink("opencode", "ses_list", 0)
	l2, _ := db.CreateShareLink("opencode", "ses_list", 0)
	_, _ = db.CreateShareLink("opencode", "ses_other", 0)

	links, err := db.ListActiveShareLinks("opencode", "ses_list")
	if err != nil {
		t.Fatalf("ListActiveShareLinks: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("expected 2 links, got %d", len(links))
	}

	// Revoke one; it should drop out of the active list.
	if _, err := db.RevokeShareLink("opencode", "ses_list", l1.Token); err != nil {
		t.Fatalf("RevokeShareLink: %v", err)
	}
	links, _ = db.ListActiveShareLinks("opencode", "ses_list")
	if len(links) != 1 {
		t.Fatalf("expected 1 active link after revoke, got %d", len(links))
	}
	if links[0].Token != l2.Token {
		t.Errorf("expected remaining link to be %q, got %q", l2.Token, links[0].Token)
	}
}

func TestExpiredShareLinkIsInactive(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	past := time.Now().Add(-time.Hour).UnixMilli()
	link, err := db.CreateShareLink("opencode", "ses_exp", past)
	if err != nil {
		t.Fatalf("CreateShareLink: %v", err)
	}
	if _, ok, _ := db.GetActiveShareLink(link.Token); ok {
		t.Fatal("expected expired link to be inactive")
	}
	links, _ := db.ListActiveShareLinks("opencode", "ses_exp")
	if len(links) != 0 {
		t.Fatalf("expected expired link excluded from active list, got %d", len(links))
	}
}

func TestFutureExpiryShareLinkIsActive(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	future := time.Now().Add(time.Hour).UnixMilli()
	link, err := db.CreateShareLink("opencode", "ses_future", future)
	if err != nil {
		t.Fatalf("CreateShareLink: %v", err)
	}
	got, ok, err := db.GetActiveShareLink(link.Token)
	if err != nil {
		t.Fatalf("GetActiveShareLink: %v", err)
	}
	if !ok {
		t.Fatal("expected future-expiry link to be active")
	}
	if got.ExpiresAt != future {
		t.Errorf("expected expiresAt %d, got %d", future, got.ExpiresAt)
	}
}
