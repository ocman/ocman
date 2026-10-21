package server

import "testing"

func TestCanonicalHistoryDoesNotInstallNativeLegacyIndexer(t *testing.T) {
	s, id := canonicalHTTPFixture(t)
	if err := s.registry.EnableCanonicalSessions(); err != nil {
		t.Fatal(err)
	}
	s.WithConversationHistory(s.historyAnalytics.Store, "owner")
	if s.sessionCatalogue != nil {
		t.Fatal("canonical history startup installed native legacy indexer")
	}
	if got, err := s.historySessionPublicID(t.Context(), "fixture", id); err == nil && got == id {
		t.Fatal("canonical history identity bypassed physical-source validation")
	}
}
