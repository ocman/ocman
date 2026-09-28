package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/NoUseFreak/ocman/internal/share"
	"github.com/NoUseFreak/ocman/internal/state"
)

// An artifact share is a relay share whose chunk 0 is a sealed JSON
// manifest and whose later chunks are the sealed raw bytes of each file,
// in manifest order. Unlike conversation shares it is written once.

// gcmOverhead is the AES-GCM tag each sealed chunk adds to its plaintext.
const gcmOverhead = 16

// defaultRelayMaxShareBytes mirrors relay.DefaultMaxShareBytes; the real
// limit is only known once the relay allocates a share.
const defaultRelayMaxShareBytes = 32 << 20

type artifactShareFile struct {
	Name     string `json:"name"`
	MIME     string `json:"mime"`
	Size     int64  `json:"size"`
	FirstSeq uint64 `json:"firstSeq"`
	Chunks   int    `json:"chunks"`
}

type artifactShareManifest struct {
	Kind        string              `json:"kind"`
	Title       string              `json:"title"`
	Description string              `json:"description,omitempty"`
	Links       []ArtifactLinkInput `json:"links"`
	Files       []artifactShareFile `json:"files"`
}

// artifactSharePlan is the chunk layout of one artifact share.
type artifactSharePlan struct {
	manifest artifactShareManifest
	raw      []byte               // manifest JSON, chunk 0 plaintext
	files    []state.ArtifactItem // file items, in manifest order
	per      int64                // plaintext bytes per file chunk
	total    int64                // sealed bytes the relay will store
}

// artifactShareLayout plans the chunks for a against the relay chunk limit.
func artifactShareLayout(a state.Artifact, maxChunkBytes int64) (artifactSharePlan, error) {
	if maxChunkBytes <= 0 {
		maxChunkBytes = 1 << 20
	}
	per := maxChunkBytes - gcmOverhead
	m := artifactShareManifest{Kind: "artifact", Title: a.Title, Description: a.Description, Links: []ArtifactLinkInput{}, Files: []artifactShareFile{}}
	var files []state.ArtifactItem
	seq, total := uint64(1), int64(0)
	for _, it := range a.Items {
		switch it.Kind {
		case state.ArtifactItemLink:
			m.Links = append(m.Links, ArtifactLinkInput{URL: it.URL, Label: it.Label})
		case state.ArtifactItemFile:
			chunks := int((it.Size + per - 1) / per)
			m.Files = append(m.Files, artifactShareFile{Name: it.Name, MIME: it.MIME, Size: it.Size, FirstSeq: seq, Chunks: chunks})
			files = append(files, it)
			seq += uint64(chunks)
			total += it.Size + int64(chunks)*gcmOverhead
		}
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return artifactSharePlan{}, err
	}
	sealedManifest := int64(len(raw)) + gcmOverhead
	if sealedManifest > maxChunkBytes {
		return artifactSharePlan{}, shareTooLarge(fmt.Sprintf("artifact manifest is %d bytes; the relay allows %d bytes per chunk", sealedManifest, maxChunkBytes))
	}
	return artifactSharePlan{manifest: m, raw: raw, files: files, per: per, total: total + sealedManifest}, nil
}

// checkArtifactShareLimits refuses a layout the relay would reject part-way.
func checkArtifactShareLimits(p artifactSharePlan, alloc share.RelayAllocation) error {
	chunks, total := 1, p.total
	for _, f := range p.manifest.Files {
		chunks += f.Chunks
	}
	if alloc.MaxShareBytes > 0 && total > alloc.MaxShareBytes {
		return shareTooLarge(fmt.Sprintf("artifact is %s (%d bytes) encrypted; the relay allows %s (%d bytes) per share",
			humanBytes(total), total, humanBytes(alloc.MaxShareBytes), alloc.MaxShareBytes))
	}
	if alloc.MaxChunks > 0 && chunks > alloc.MaxChunks {
		return shareTooLarge(fmt.Sprintf("artifact needs %d chunks; the relay allows %d per share", chunks, alloc.MaxChunks))
	}
	return nil
}

func humanBytes(n int64) string {
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}

var errNoShareRelay = errors.New("no share relay is configured")

// publishArtifactShare uploads a to a fresh relay share and records it. Any
// failure after allocation deletes the relay share so nothing half-written
// stays reachable.
func (s *Server) publishArtifactShare(ctx context.Context, a state.Artifact) (state.ArtifactShare, error) {
	if s.relayURL == "" {
		return state.ArtifactShare{}, errNoShareRelay
	}
	client := s.relayClient(s.relayURL)
	alloc, err := client.Create(ctx)
	if err != nil {
		return state.ArtifactShare{}, err
	}
	rec, err := s.uploadArtifactShare(ctx, client, alloc, a)
	if err != nil {
		_ = client.Delete(context.WithoutCancel(ctx), alloc)
	}
	return rec, err
}

func (s *Server) uploadArtifactShare(ctx context.Context, client share.RelayClient, alloc share.RelayAllocation, a state.Artifact) (state.ArtifactShare, error) {
	p, err := artifactShareLayout(a, alloc.MaxChunkBytes)
	if err != nil {
		return state.ArtifactShare{}, err
	}
	if err := checkArtifactShareLimits(p, alloc); err != nil {
		return state.ArtifactShare{}, err
	}
	key, err := share.NewKey()
	if err != nil {
		return state.ArtifactShare{}, err
	}
	put := func(seq uint64, plain []byte) error {
		sealed, err := share.Seal(key, alloc.ID, seq, plain)
		if err != nil {
			return err
		}
		return client.Put(ctx, alloc, seq, sealed)
	}
	if err := s.eachArtifactShareChunk(p, put); err != nil {
		return state.ArtifactShare{}, err
	}
	return s.stateDB.CreateArtifactShare(ctx, state.ArtifactShare{
		ArtifactID: a.ID, RelayID: alloc.ID, RelayKey: key.String(), RelayDeleteToken: alloc.DeleteToken, RelayURL: s.relayURL,
	})
}

// eachArtifactShareChunk emits every plaintext chunk of p in sequence
// order: the manifest at 0, then each file's bytes split at p.per.
func (s *Server) eachArtifactShareChunk(p artifactSharePlan, emit func(uint64, []byte) error) error {
	if err := emit(0, p.raw); err != nil {
		return err
	}
	buf := make([]byte, p.per)
	for i, it := range p.files {
		if err := s.emitArtifactFile(it, p.manifest.Files[i], buf, emit); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) emitArtifactFile(it state.ArtifactItem, f artifactShareFile, buf []byte, put func(uint64, []byte) error) error {
	blob, err := s.stateDB.OpenArtifactBlob(it.SHA256)
	if err != nil {
		return err
	}
	defer blob.Close()
	for c := range f.Chunks {
		n, err := io.ReadFull(blob, buf)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("reading %s: %w", it.Name, err)
		}
		if err := put(f.FirstSeq+uint64(c), buf[:n]); err != nil {
			return err
		}
	}
	return nil
}

type artifactShareView struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	CreatedAt int64  `json:"createdAt"`
	RevokedAt int64  `json:"revokedAt,omitempty"`
}

func viewArtifactShare(sh state.ArtifactShare) artifactShareView {
	return artifactShareView{
		ID: sh.ID, CreatedAt: sh.CreatedAt, RevokedAt: sh.RevokedAt,
		URL: strings.TrimRight(sh.RelayURL, "/") + "/v/" + sh.RelayID + "#k=" + sh.RelayKey,
	}
}

func writeArtifactShareError(w http.ResponseWriter, relayURL string, err error) {
	var relayErr *share.RelayError
	switch {
	case errors.Is(err, errNoShareRelay):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.As(err, &relayErr) && relayErr.Status == http.StatusRequestEntityTooLarge:
		http.Error(w, "this artifact is too large for the share relay: "+relayErr.Message, http.StatusRequestEntityTooLarge)
	case errors.Is(err, state.ErrArtifactNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		writeShareRelayError(w, relayURL, err)
	}
}

func (s *Server) handleArtifactShareCreate(w http.ResponseWriter, r *http.Request, id string) {
	a, err := s.stateDB.GetArtifact(r.Context(), id)
	if err != nil {
		writeArtifactError(w, "getting artifact", err)
		return
	}
	sh, err := s.publishArtifactShare(r.Context(), a)
	if err != nil {
		writeArtifactShareError(w, s.relayURL, err)
		return
	}
	writeJSONStatus(w, http.StatusCreated, viewArtifactShare(sh))
}

func (s *Server) handleArtifactShareList(w http.ResponseWriter, r *http.Request, id string) {
	list, err := s.stateDB.ListArtifactShares(r.Context(), id)
	if err != nil {
		serverError(w, "listing artifact shares", err)
		return
	}
	views := make([]artifactShareView, 0, len(list))
	for _, sh := range list {
		views = append(views, viewArtifactShare(sh))
	}
	writeJSON(w, map[string]any{"shares": views, "relayConfigured": s.relayURL != "", "maxShareBytes": defaultRelayMaxShareBytes})
}

// revokeArtifactShare deletes the relay share, then stamps revoked_at.
func (s *Server) revokeArtifactShare(ctx context.Context, sh state.ArtifactShare) error {
	if sh.RevokedAt != 0 {
		return nil
	}
	if err := s.relayClient(sh.RelayURL).Delete(ctx, share.RelayAllocation{ID: sh.RelayID, DeleteToken: sh.RelayDeleteToken}); err != nil {
		return err
	}
	return s.stateDB.RevokeArtifactShare(ctx, sh.ArtifactID, sh.ID)
}

func (s *Server) handleArtifactShareRevoke(w http.ResponseWriter, r *http.Request, id, shareID string) {
	sh, err := s.stateDB.GetArtifactShare(r.Context(), id, shareID)
	if err == nil {
		err = s.revokeArtifactShare(r.Context(), sh)
	}
	if err != nil {
		writeArtifactShareError(w, sh.RelayURL, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// revokeAllArtifactShares takes every live relay copy of an artifact down
// before it is deleted locally, which would otherwise lose the tokens.
func (s *Server) revokeAllArtifactShares(ctx context.Context, id string) error {
	list, err := s.stateDB.ListArtifactShares(ctx, id)
	if err != nil {
		return err
	}
	for _, sh := range list {
		if err := s.revokeArtifactShare(ctx, sh); err != nil {
			return err
		}
	}
	return nil
}
