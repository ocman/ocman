package remote

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type metadataPlatform struct {
	*fakePlatform
	source    platforms.HistoryIdentity
	sourceErr error
	keys      []string
}

func (p *metadataPlatform) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return p.source, p.sourceErr
}
func (p *metadataPlatform) ReadSourceKeys() []string { return append([]string(nil), p.keys...) }

type metadataOwner struct {
	connectionLifecycleOwner
	inventory hostsvc.ConnectionInventory
}

func (h *metadataOwner) InventoryPlatformConnections(context.Context) (hostsvc.ConnectionInventory, error) {
	return h.inventory, h.err
}

func TestConnectionMetadataFailsClosedOnUnprovenSourcesAndInventoryErrors(t *testing.T) {
	owner := &metadataOwner{inventory: hostsvc.ConnectionInventory{Supported: true, Connections: []hostsvc.ConnectionView{{ID: "cnx_work", Name: "Verified work account", IsDefault: true, Enabled: true, Status: "ready"}}}}
	p := &metadataPlatform{fakePlatform: &fakePlatform{id: "codex"}, source: platforms.HistoryIdentity{Platform: "codex", SourceKey: "active"}, keys: []string{"active", "retained"}}
	registry := platforms.NewRegistry()
	if err := registry.RegisterConnection("cnx_work", p); err != nil {
		t.Fatal(err)
	}
	s := NewServer(registry, owner, "owner", "test")
	response, err := s.ConnectionPlatforms(t.Context(), &pb.CallReq{})
	if err != nil {
		t.Fatal(err)
	}
	var entries []ConnectionPlatformMetadata
	if err := unmarshalJSON(response.Payload, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].DisplayName != "Verified work account" || !entries[0].Default || !entries[0].Selectable || len(entries[0].SourceKeys) != 2 {
		t.Fatal(entries)
	}
	p.sourceErr = errors.New("source verification failed")
	if _, err := s.ConnectionPlatforms(t.Context(), &pb.CallReq{}); err == nil {
		t.Fatal("failed source verification advertised authority")
	}
	p.sourceErr = nil
	p.source.Platform = "opencode"
	if _, err := s.ConnectionPlatforms(t.Context(), &pb.CallReq{}); err == nil {
		t.Fatal("wrong physical kind advertised authority")
	}
	p.source.Platform = "codex"
	p.keys = []string{"retained"}
	if _, err := s.ConnectionPlatforms(t.Context(), &pb.CallReq{}); err == nil {
		t.Fatal("allowlist missing active source accepted")
	}
	p.keys = []string{"active"}
	owner.err = errors.New("owner inventory unavailable")
	if _, err := s.ConnectionPlatforms(t.Context(), &pb.CallReq{}); err == nil {
		t.Fatal("failed owner inventory advertised readiness")
	}
}

func TestHandshakeMetadataRejectsMalformedAndMismatchedConnectionInventories(t *testing.T) {
	for _, tt := range []struct {
		name   string
		client boundaryClient
		keys   []string
	}{
		{"transport", boundaryClient{err: status.Error(codes.Unavailable, "offline")}, []string{"cnx_work"}},
		{"malformed", boundaryClient{payload: []byte(`{`)}, []string{"cnx_work"}},
		{"count", boundaryClient{payload: []byte(`[]`)}, []string{"cnx_work"}},
		{"key", boundaryClient{payload: []byte(`[{"id":"cnx_sibling","kind":"codex"}]`)}, []string{"cnx_work"}},
		{"kind", boundaryClient{payload: []byte(`[{"id":"cnx_work","kind":""}]`)}, []string{"cnx_work"}},
		{"owner-prefix", boundaryClient{payload: []byte(`[{"id":"r-other:cnx_work","kind":"codex"}]`)}, []string{"r-other:cnx_work"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := loadConnectionPlatforms(t.Context(), tt.client, tt.keys); err == nil {
				t.Fatal("unverified connection inventory accepted")
			}
		})
	}
}
