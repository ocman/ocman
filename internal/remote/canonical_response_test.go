package remote

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/google/uuid"
)

func canonicalRemotePlatform() *remotePlatform {
	return newRemotePlatform(&RemoteConn{protocolVersion: canonicalSessionProtocol, remoteID: "owner"}, "opencode", func() string { return "owner" })
}

func TestCanonicalRemoteResponsesRejectNativeRowsBeforeCachingOwnership(t *testing.T) {
	p := canonicalRemotePlatform()
	if rows, err := p.checkedSessions([]db.Session{{ID: "native"}}, "", 0); !errors.Is(err, platforms.ErrPlatformUnreachable) || rows != nil {
		t.Fatalf("native inventory escaped integration: %+v %v", rows, err)
	}
	if len(p.owned) != 0 {
		t.Fatal("invalid peer inventory acquired cache ownership")
	}
	id := "ocm_" + uuid.NewString()
	rows, err := p.checkedSessions([]db.Session{{ID: id, Platform: "opencode"}}, "", 0)
	if err != nil || len(rows) != 1 || rows[0].ID != id || rows[0].Platform != string(p.ID()) {
		t.Fatalf("canonical inventory rejected: %+v %v", rows, err)
	}
	if cached, err := p.checkedStaleSessions("", 0); err != nil || len(cached) != 1 || cached[0].ID != id || !cached[0].Stale {
		t.Fatalf("canonical offline cache changed: %+v %v", cached, err)
	}
	p.lastSess = []db.Session{{ID: "native"}}
	if rows, err := p.checkedStaleSessions("", 0); !errors.Is(err, platforms.ErrPlatformUnreachable) || rows != nil {
		t.Fatalf("old cache reintroduced native ID: %+v %v", rows, err)
	}
	if rows, err := p.checkedArchive([]db.SessionArchiveCandidate{{ID: "native"}}); !errors.Is(err, platforms.ErrPlatformUnreachable) || rows != nil {
		t.Fatalf("native archive candidate accepted: %+v %v", rows, err)
	}
}

func TestCanonicalRemoteDetailPreservesMessagePartAndOpaqueProviderIDs(t *testing.T) {
	p := canonicalRemotePlatform()
	id := "ocm_" + uuid.NewString()
	detail := &platforms.SessionDetail{Session: &db.Session{ID: id, Platform: "opencode"}, Messages: []db.Message{{ID: "native-message", SessionID: id}}, Parts: []db.Part{{ID: "native-part", MessageID: "native-message", SessionID: id, Data: json.RawMessage(`{"sessionID":"opaque-native"}`)}}}
	got, err := p.checkedDetail(detail, id)
	if err != nil || got.Messages[0].ID != "native-message" || got.Parts[0].ID != "native-part" || string(got.Parts[0].Data) != `{"sessionID":"opaque-native"}` {
		t.Fatalf("native content IDs changed: %+v %v", got, err)
	}
	detail.Parts[0].SessionID = "native"
	if _, err := p.checkedDetail(detail, id); !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatalf("native normalized part reference accepted: %v", err)
	}
	if _, err := p.checkedSummary(&db.Session{ID: "native"}, id); !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatalf("native summary accepted: %v", err)
	}
	if _, err := canonicalSliceResult(p, []platforms.LivePrompt{{"id": "permission", "sessionID": "native"}}); !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatalf("native prompt reference accepted: %v", err)
	}
	prompts, err := canonicalSliceResult(p, []platforms.LivePrompt{{"id": "native-permission", "sessionID": id, "metadata": map[string]any{"sessionID": "opaque-native"}}})
	if err != nil || prompts[0]["id"] != "native-permission" || prompts[0]["metadata"].(map[string]any)["sessionID"] != "opaque-native" {
		t.Fatalf("prompt metadata changed: %+v %v", prompts, err)
	}
}

func TestInvalidCanonicalCreationReplyRetainsUncertainOutcomeWithoutNativeHandle(t *testing.T) {
	p := canonicalRemotePlatform()
	for _, payload := range []string{`{"id":"native"}`, `invalid JSON`} {
		response, err := canonicalDecodeResult(p, []byte(payload), &platforms.CreateSessionResponse{})
		var pending *platforms.CreationPendingError
		if !errors.As(err, &pending) || pending.SessionID != "" || response != nil {
			t.Fatalf("invalid creation reply gained authority or lost uncertainty: %+v %v", response, err)
		}
	}
	id := "ocm_" + uuid.NewString()
	encoded, _ := marshalJSON(platforms.CreateSessionResponse{ID: id, Replayed: true})
	response, err := canonicalDecodeResult(p, encoded, &platforms.CreateSessionResponse{})
	if err != nil || response.ID != id || !response.Replayed {
		t.Fatalf("creation replay receipt changed: %+v %v", response, err)
	}
}
