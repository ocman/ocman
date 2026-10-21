package sessionview

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms/identity"
)

func TestPendingFeatureReferenceRetainsRecordWithoutPublishingExecutableLink(t *testing.T) {
	ref := identity.PendingReference()
	raw, _ := json.Marshal(map[string]any{"id": "routine", "sessionId": ref, "data": map[string]string{"sessionId": ref}})
	p := Projector{Shape: ReferenceShape, Lookup: func(context.Context, string, []string) (map[string]string, error) {
		t.Fatal("pending reference queried disconnected owner")
		return nil, nil
	}}
	out, err := p.JSON(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(out, &value); err != nil {
		t.Fatal(err)
	}
	if value["id"] != "routine" || value["sessionId"] != "" || value["data"].(map[string]any)["sessionId"] != ref {
		t.Fatalf("pending reference altered record or opaque data: %s", out)
	}
}

func TestPendingReferenceIsNotAnInventoriedSession(t *testing.T) {
	raw, _ := json.Marshal([]map[string]string{{"id": identity.PendingReference()}, {"id": "ocm_ready"}})
	p := Projector{Shape: SessionShape, OmitUnresolvedRows: true, Lookup: func(context.Context, string, []string) (map[string]string, error) {
		t.Fatal("pending session reference queried owner")
		return nil, nil
	}}
	out, err := p.JSON(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]string
	if err := json.Unmarshal(out, &rows); err != nil || len(rows) != 1 || rows[0]["id"] != "ocm_ready" {
		t.Fatalf("pending reference became a session: %s %v", out, err)
	}
}
