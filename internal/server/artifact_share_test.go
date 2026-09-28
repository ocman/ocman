package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/relay"
	"github.com/NoUseFreak/ocman/internal/share"
	"github.com/NoUseFreak/ocman/internal/state"
)

func artifactRelay(t *testing.T, cfg relay.Config) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := share.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Store = store
	srv, err := relay.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts, dir
}

// readArtifactRelay returns the decrypted plaintext of every chunk.
func readArtifactRelay(t *testing.T, shareURL string) (string, [][]byte, int) {
	t.Helper()
	base, frag, _ := strings.Cut(shareURL, "#k=")
	origin, id, _ := strings.Cut(base, "/v/")
	key, err := share.ParseKey(frag)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(origin + "/s/" + id + "?from=0")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return id, nil, resp.StatusCode
	}
	var body struct {
		Chunks []struct {
			Seq  uint64 `json:"seq"`
			Data string `json:"data"`
		} `json:"chunks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	var out [][]byte
	for i, c := range body.Chunks {
		if c.Seq != uint64(i) {
			t.Fatalf("chunk %d has seq %d", i, c.Seq)
		}
		raw, _ := base64.RawStdEncoding.DecodeString(c.Data)
		plain, err := share.Open(key, id, c.Seq, raw)
		if err != nil {
			t.Fatalf("open %d: %v", c.Seq, err)
		}
		out = append(out, plain)
	}
	return id, out, http.StatusOK
}

func TestArtifactShareLayout(t *testing.T) {
	a := state.Artifact{Title: "T", Description: "D", Items: []state.ArtifactItem{
		{Kind: state.ArtifactItemFile, Name: "a.txt", MIME: "text/plain", Size: 10},
		{Kind: state.ArtifactItemLink, URL: "https://example.com", Label: "ex"},
		{Kind: state.ArtifactItemFile, Name: "b.bin", MIME: "application/octet-stream", Size: 25},
		{Kind: state.ArtifactItemFile, Name: "empty", MIME: "text/plain", Size: 0},
	}}
	if _, err := artifactShareLayout(a, 10+gcmOverhead); err == nil {
		t.Fatal("manifest larger than a 26-byte chunk must be refused")
	}
	p, err := artifactShareLayout(a, 1<<10)
	if err != nil {
		t.Fatal(err)
	}
	if p.per != 1<<10-gcmOverhead || len(p.files) != 3 || len(p.manifest.Links) != 1 || p.manifest.Kind != "artifact" {
		t.Fatalf("plan = %+v", p)
	}
	// Force a small per-chunk size to exercise the seq arithmetic.
	a.Items[0].Size, a.Items[2].Size = 2000, 3000
	p, _ = artifactShareLayout(a, 1<<10)
	got := p.manifest.Files
	if got[0].FirstSeq != 1 || got[0].Chunks != 2 || got[1].FirstSeq != 3 || got[1].Chunks != 3 || got[2].FirstSeq != 6 || got[2].Chunks != 0 {
		t.Fatalf("files = %+v", got)
	}
	if want := int64(len(p.raw)+gcmOverhead) + 5000 + 5*gcmOverhead; p.total != want {
		t.Fatalf("total = %d, want %d", p.total, want)
	}
	if err := checkArtifactShareLimits(p, share.RelayAllocation{MaxChunks: 5}); err == nil {
		t.Fatal("6 chunks must exceed MaxChunks=5")
	}
	if err := checkArtifactShareLimits(p, share.RelayAllocation{MaxChunks: 6, MaxShareBytes: p.total}); err != nil {
		t.Fatal(err)
	}
}

func TestArtifactShareRoundTripAndRevoke(t *testing.T) {
	ts, _ := artifactRelay(t, relay.Config{MaxChunkBytes: 256})
	srv, do := artifactServer(t)
	srv.WithRelay(ts.URL, "flag")
	body := strings.Repeat("0123456789", 100) // 1000 bytes -> 5 chunks of 240
	a := mustArtifact(t, srv, ArtifactInput{Title: "Report", Files: []ArtifactFileInput{{Name: "r.txt", Content: body}}, Links: []ArtifactLinkInput{{URL: "https://example.com"}}})

	if rec := do(http.MethodGet, "/api/artifacts/"+a.ID+"/share"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET share = %d", rec.Code)
	}
	rec := do(http.MethodPost, "/api/artifacts/"+a.ID+"/share")
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	var view artifactShareView
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if !strings.HasPrefix(view.URL, ts.URL+"/v/") || !strings.Contains(view.URL, "#k=") {
		t.Fatalf("url = %q", view.URL)
	}
	_, chunks, _ := readArtifactRelay(t, view.URL)
	var m artifactShareManifest
	if err := json.Unmarshal(chunks[0], &m); err != nil {
		t.Fatal(err)
	}
	if m.Kind != "artifact" || m.Title != "Report" || len(m.Links) != 1 || len(m.Files) != 1 || m.Files[0].Chunks != 5 || len(chunks) != 6 {
		t.Fatalf("manifest = %+v, chunks = %d", m, len(chunks))
	}
	if got := bytes.Join(chunks[1:], nil); string(got) != body {
		t.Fatalf("file bytes = %q", got)
	}

	rec = do(http.MethodGet, "/api/artifacts/"+a.ID+"/shares")
	var list struct {
		Shares          []artifactShareView `json:"shares"`
		RelayConfigured bool                `json:"relayConfigured"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if !list.RelayConfigured || len(list.Shares) != 1 || list.Shares[0].URL != view.URL {
		t.Fatalf("list = %s", rec.Body)
	}

	for range 2 { // revoking twice is idempotent
		if rec := do(http.MethodDelete, "/api/artifacts/"+a.ID+"/share/"+view.ID); rec.Code != http.StatusNoContent {
			t.Fatalf("revoke = %d %s", rec.Code, rec.Body)
		}
	}
	if _, _, code := readArtifactRelay(t, view.URL); code != http.StatusNotFound {
		t.Fatalf("relay after revoke = %d", code)
	}
	rec = do(http.MethodGet, "/api/artifacts/"+a.ID+"/shares")
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if list.Shares[0].RevokedAt == 0 {
		t.Fatalf("revokedAt unset: %s", rec.Body)
	}
	if rec := do(http.MethodDelete, "/api/artifacts/"+a.ID+"/share/nope"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown share = %d", rec.Code)
	}
}

func TestArtifactShareRefusesOversizeAndDeletesAllocation(t *testing.T) {
	ts, dir := artifactRelay(t, relay.Config{MaxShareBytes: 1000})
	srv, do := artifactServer(t)
	srv.WithRelay(ts.URL, "flag")
	a := mustArtifact(t, srv, ArtifactInput{Title: "Big", Files: []ArtifactFileInput{{Name: "big.txt", Content: strings.Repeat("x", 2000)}}})

	rec := do(http.MethodPost, "/api/artifacts/"+a.ID+"/share")
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "(1000 bytes)") {
		t.Fatalf("oversize = %d %s", rec.Code, rec.Body)
	}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			t.Errorf("relay kept %s after refusal", path)
		}
		return nil
	})
	if list, _ := srv.stateDB.ListArtifactShares(t.Context(), a.ID); len(list) != 0 {
		t.Fatalf("refused share was recorded: %+v", list)
	}
}

func TestArtifactShareWithoutRelayAndDeleteRevokes(t *testing.T) {
	srv, do := artifactServer(t)
	a := mustArtifact(t, srv, ArtifactInput{Title: "L", Links: []ArtifactLinkInput{{URL: "https://example.com"}}})
	if rec := do(http.MethodPost, "/api/artifacts/"+a.ID+"/share"); rec.Code != http.StatusConflict {
		t.Fatalf("no relay = %d", rec.Code)
	}
	if rec := do(http.MethodPost, "/api/artifacts/missing/share"); rec.Code != http.StatusNotFound {
		t.Fatalf("missing artifact = %d", rec.Code)
	}

	ts, _ := artifactRelay(t, relay.Config{})
	srv.WithRelay(ts.URL, "flag")
	rec := do(http.MethodPost, "/api/artifacts/"+a.ID+"/share")
	var view artifactShareView
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if rec := do(http.MethodDelete, "/api/artifacts/"+a.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
	if _, _, code := readArtifactRelay(t, view.URL); code != http.StatusNotFound {
		t.Fatalf("relay after artifact delete = %d", code)
	}
}

// TestArtifactShareFixture seals a known artifact with a fixed key and pins
// it as the fixture the browser viewer's tests decrypt, so both sides of
// the wire format are checked against the same bytes. Regenerate with
// OCMAN_UPDATE_FIXTURES=1.
func TestArtifactShareFixture(t *testing.T) {
	srv, _ := artifactServer(t)
	png := []byte("\x89PNG\r\n\x1a\nnot-really-an-image")
	a := mustArtifact(t, srv, ArtifactInput{Title: "Fixture", Description: "Sealed in Go", Files: []ArtifactFileInput{
		{Name: "notes.md", Content: "# Notes\n\nhello **world**\n"},
		{Name: "pixel.png", Content: string(png)},
		{Name: "empty.txt", Content: ""},
	}, Links: []ArtifactLinkInput{{URL: "https://example.com/pr/1", Label: "PR"}}})
	p, err := artifactShareLayout(a, 1<<10)
	if err != nil {
		t.Fatal(err)
	}
	p.per = 16 // split every file across chunks; the manifest agrees below
	seq := uint64(1)
	for i := range p.manifest.Files {
		f := &p.manifest.Files[i]
		f.FirstSeq, f.Chunks = seq, int((f.Size+15)/16)
		seq += uint64(f.Chunks)
	}
	p.raw, _ = json.Marshal(p.manifest)

	var key share.Key
	for i := range key {
		key[i] = byte(i)
	}
	const relayID = "20260928-fixture"
	type chunk struct {
		Seq  uint64 `json:"seq"`
		Data string `json:"data"`
	}
	fixture := struct {
		ID     string  `json:"id"`
		Key    string  `json:"key"`
		Chunks []chunk `json:"chunks"`
	}{ID: relayID, Key: key.String()}
	err = srv.eachArtifactShareChunk(p, func(seq uint64, plain []byte) error {
		sealed, err := share.Seal(key, relayID, seq, plain)
		fixture.Chunks = append(fixture.Chunks, chunk{seq, base64.RawStdEncoding.EncodeToString(sealed)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fixture.Chunks) != int(seq) {
		t.Fatalf("emitted %d chunks, manifest declares %d", len(fixture.Chunks), seq)
	}
	got, _ := json.MarshalIndent(fixture, "", "  ")
	got = append(got, '\n')
	path := filepath.Join("..", "..", "frontend", "src", "lib", "artifactShare.fixture.json")
	if os.Getenv("OCMAN_UPDATE_FIXTURES") == "1" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (regenerate with OCMAN_UPDATE_FIXTURES=1)", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("artifact share wire format changed; regenerate the fixture with OCMAN_UPDATE_FIXTURES=1 and update the viewer")
	}
}
