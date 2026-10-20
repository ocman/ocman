package sessionview

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func lookup(_ context.Context, platform string, ids []string) (map[string]string, error) {
	out := map[string]string{}
	for _, id := range ids {
		out[id] = "ocm_" + platform + "_" + id
	}
	return out, nil
}

func TestProjectNormalizedReferencesWithoutRewritingNativePayload(t *testing.T) {
	p := Projector{Lookup: lookup, Platform: "test"}
	raw := []byte(`{"session":{"id":"root","parentId":"parent","platform":"test"},"messages":[{"id":"message","sessionId":"root","data":{"parentID":"message-parent","sessionID":"root"}}],"parts":[{"id":"part","sessionId":"root","data":{"type":"text","text":"root","input":{"sessionId":"root"}}}],"sessionTree":[{"id":"child","parentId":"root"}]}`)
	projected, err := p.JSON(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(projected, &value); err != nil {
		t.Fatal(err)
	}
	if session := value["session"].(map[string]any); session["id"] != "ocm_test_root" || session["parentId"] != "ocm_test_parent" {
		t.Fatalf("session refs: %s", projected)
	}
	message := value["messages"].([]any)[0].(map[string]any)
	if message["id"] != "message" || message["sessionId"] != "ocm_test_root" || message["data"].(map[string]any)["parentID"] != "message-parent" || message["data"].(map[string]any)["sessionID"] != "root" {
		t.Fatalf("native message changed: %s", projected)
	}
	part := value["parts"].([]any)[0].(map[string]any)
	if part["id"] != "part" || part["data"].(map[string]any)["input"].(map[string]any)["sessionId"] != "root" {
		t.Fatalf("opaque tool data changed: %s", projected)
	}
	child := value["sessionTree"].([]any)[0].(map[string]any)
	if child["id"] != "ocm_test_child" || child["parentId"] != "ocm_test_root" {
		t.Fatalf("tree refs: %s", projected)
	}
}

func TestProjectBatchesByOwnerAndBoundsRequests(t *testing.T) {
	rows := make([]map[string]any, 1001)
	for i := range rows {
		rows[i] = map[string]any{"id": fmt.Sprint(i), "platform": "owner", "directory": "/repo", "timeCreated": 1, "timeUpdated": 2}
	}
	raw, _ := json.Marshal(rows)
	calls := 0
	p := Projector{Shape: SessionShape, OmitUnresolvedRows: true, Lookup: func(ctx context.Context, platform string, ids []string) (map[string]string, error) {
		calls++
		if platform != "owner" || len(ids) > 500 {
			t.Fatalf("unbounded/incorrect scope: %s %d", platform, len(ids))
		}
		return lookup(ctx, platform, ids)
	}}
	if _, err := p.JSON(t.Context(), raw); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("projection made %d requests for 1001 sessions", calls)
	}
	if _, err := p.JSON(t.Context(), raw); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("repeated frame re-resolved stable identities")
	}
}

func TestProjectOceEnvelopeKeepsCursorAndNativeMessageIDs(t *testing.T) {
	p := Projector{Lookup: lookup}
	raw := []byte(`{"epoch":"epoch","seq":9007199254740993,"platform":"remote","sessionId":"root","event":{"type":"child.upserted","child":{"id":"child","parentId":"root","title":"new"}}}`)
	result, err := p.JSON(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"seq":9007199254740993`, `"epoch":"epoch"`, `"sessionId":"ocm_remote_root"`, `"id":"ocm_remote_child"`, `"parentId":"ocm_remote_root"`} {
		if !strings.Contains(string(result), expected) {
			t.Fatalf("missing %s: %s", expected, result)
		}
	}
}

func TestProjectListOmitsPendingIdentityWithoutLeakingNativeIDs(t *testing.T) {
	p := Projector{Shape: SessionShape, OmitUnresolvedRows: true, Platform: "test", Lookup: func(context.Context, string, []string) (map[string]string, error) {
		return map[string]string{"ready": "ocm_ready"}, nil
	}}
	raw := []byte(`[{"id":"ready"},{"id":"pending"}]`)
	result, err := p.JSON(t.Context(), raw)
	if err != nil || string(result) != `[{"id":"ocm_ready"}]` {
		t.Fatalf("pending row broke healthy list: %s %v", result, err)
	}
	p.OmitUnresolvedRows = false
	if _, err := p.JSON(t.Context(), []byte(`{"sessionId":"pending"}`)); err == nil {
		t.Fatal("pending detail must fail closed")
	}
}

func TestProjectOnlyVisitsDeclaredWireFields(t *testing.T) {
	p := Projector{Lookup: lookup, Platform: "test"}
	raw := []byte(`{"sessionId":"root","unrelated":{"sessionId":"opaque","id":"user-id","parentID":"message"},"data":{"session":{"id":"opaque"}},"event":{"type":"future.event","sessionId":"future"}}`)
	result, err := p.JSON(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"sessionId":"ocm_test_root"`, `"sessionId":"opaque"`, `"id":"user-id"`, `"parentID":"message"`, `"sessionId":"future"`} {
		if !strings.Contains(string(result), expected) {
			t.Fatalf("opaque reference rewritten: %s", result)
		}
	}
}

func TestProjectOwnerQualifiedChildrenDoesNotUseLocalCollidingNativeID(t *testing.T) {
	shape := &Shape{OwnerField: "remoteId", Children: map[string]*Shape{"session": ReferenceShape}}
	p := Projector{Lookup: lookup, Shape: shape}
	result, err := p.JSON(t.Context(), []byte(`{"remoteId":"machine","session":{"platform":"test","sessionId":"same"}}`))
	if err != nil || !strings.Contains(string(result), `"sessionId":"ocm_r-machine:test_same"`) {
		t.Fatalf("remote ownership lost: %s %v", result, err)
	}
}

func TestProjectionShapeAndIncompleteRowPolicyAreIndependent(t *testing.T) {
	for _, tc := range []struct {
		name      string
		shape     *Shape
		raw, want string
	}{
		{"session detail", SessionShape, `{"id":"ready"}`, `{"id":"ocm_ready"}`},
		{"reference list", ReferenceShape, `[{"id":"artifact","sessionId":"ready"},{"id":"other","sessionId":"pending"}]`, `[{"id":"artifact","sessionId":"ocm_ready"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := Projector{Shape: tc.shape, OmitUnresolvedRows: true, Lookup: func(context.Context, string, []string) (map[string]string, error) {
				return map[string]string{"ready": "ocm_ready"}, nil
			}}
			result, err := p.JSON(t.Context(), []byte(tc.raw))
			if err != nil || string(result) != tc.want {
				t.Fatalf("projection: %s, %v", result, err)
			}
			if _, err := p.JSON(t.Context(), []byte(`{"sessionId":"pending"}`)); err == nil {
				t.Fatal("row omission must never hide an unresolved object")
			}
		})
	}
}

func TestProjectRemoteHelloOverlayOnColdConnectAndReconnect(t *testing.T) {
	p := Projector{Lookup: lookup}
	raw := []byte(`{"platform":"r-owner:test","sessionId":"root","event":{"type":"stream.hello","overlay":{"prompts":[{"id":"permission","sessionId":"child","metadata":{"sessionId":"opaque"}}],"inflight":{"messages":[{"id":"message","sessionId":"root"}],"parts":[{"id":"part","messageId":"message","sessionId":"root","data":{"sessionId":"opaque"}}]}}}}`)
	for range 2 {
		result, err := p.JSON(t.Context(), raw)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`"sessionId":"ocm_r-owner:test_child"`, `"sessionId":"ocm_r-owner:test_root"`, `"id":"permission"`, `"id":"message"`, `"id":"part"`, `"messageId":"message"`, `"metadata":{"sessionId":"opaque"}`, `"data":{"sessionId":"opaque"}`} {
			if !strings.Contains(string(result), want) {
				t.Fatalf("hello overlay lost %s: %s", want, result)
			}
		}
		if strings.Contains(string(result), `"sessionId":"root"`) {
			t.Fatalf("hello inflight leaked native reference: %s", result)
		}
	}
}
