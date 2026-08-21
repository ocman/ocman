// Package relay implements ocman's cross-machine conversation share
// relay: a blind, append-only store for encrypted conversation chunks.
//
// The relay never sees plaintext. It holds ciphertext sealed by the
// sharing ocman instance, and the key travels only in the viewer URL's
// fragment, which browsers do not send to servers. A relay operator, or
// anyone who compromises one, learns object sizes and timestamps and
// nothing else.
//
// It also holds no credential it could replay: the delete token that
// authorises appends and revocation is stored only as a SHA-256 hash.
//
// Sequence numbers are allocated by the writer, never by the relay, so
// there is no counter to serialise and appends are idempotent.
package relay

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/share"
)

// Defaults for the storage limits. They exist because share creation is
// unauthenticated: the relay is protected by caps, not credentials.
const (
	DefaultMaxChunkBytes = 1 << 20  // 1 MiB per chunk
	DefaultMaxChunks     = 4096     // chunks per share
	DefaultMaxShareBytes = 32 << 20 // 32 MiB total per share
	DefaultTTL           = 30 * 24 * time.Hour
	DefaultCreatePerHour = 60
	DefaultCreateBurst   = 10
)

// Config configures a relay server.
type Config struct {
	// Store is the object storage backend. Required.
	Store share.Store
	// Assets is the embedded single-page app used to render a shared
	// conversation. Optional: with no assets the relay serves the API
	// only.
	Assets fs.FS
	// MaxChunkBytes caps one appended chunk.
	MaxChunkBytes int64
	// MaxChunks caps the number of chunks in one share.
	MaxChunks int
	// MaxShareBytes caps the total stored bytes of one share.
	MaxShareBytes int64
	// TTL is how long a share is retained.
	TTL time.Duration
	// CreatePerHour and CreateBurst rate-limit share creation per client
	// address.
	CreatePerHour float64
	CreateBurst   float64
	// TrustProxy makes the server read the client address from
	// X-Forwarded-For. Only enable it behind a proxy that overwrites
	// that header, or clients can forge their rate-limit identity.
	TrustProxy bool
	// Now is the clock, overridable in tests.
	Now func() time.Time
}

// Server is the relay's HTTP handler.
type Server struct {
	cfg     Config
	mux     *http.ServeMux
	creates *rateLimiter
	// ponytail: one relay-wide lock keeps quota checks and mutations atomic;
	// use per-share/store transactions if append throughput ever matters.
	mutations sync.Mutex
}

// New builds a relay server, applying defaults for unset limits.
func New(cfg Config) (*Server, error) {
	if cfg.Store == nil {
		return nil, errors.New("relay: a store is required")
	}
	if cfg.MaxChunkBytes <= 0 {
		cfg.MaxChunkBytes = DefaultMaxChunkBytes
	}
	if cfg.MaxChunks <= 0 {
		cfg.MaxChunks = DefaultMaxChunks
	}
	if cfg.MaxShareBytes <= 0 {
		cfg.MaxShareBytes = DefaultMaxShareBytes
	}
	if cfg.TTL <= 0 {
		cfg.TTL = DefaultTTL
	}
	if cfg.CreatePerHour <= 0 {
		cfg.CreatePerHour = DefaultCreatePerHour
	}
	if cfg.CreateBurst <= 0 {
		cfg.CreateBurst = DefaultCreateBurst
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.MaxChunks > int(share.MaxSeq)+1 {
		return nil, fmt.Errorf("relay: MaxChunks %d exceeds the addressable sequence space", cfg.MaxChunks)
	}

	s := &Server{
		cfg:     cfg,
		mux:     http.NewServeMux(),
		creates: newRateLimiter(cfg.CreatePerHour, cfg.CreateBurst, cfg.Now),
	}
	s.routes()
	return s, nil
}

func (s *Server) routes() {
	s.mux.HandleFunc("POST /s", s.handleCreate)
	s.mux.HandleFunc("GET /s/{id}", s.handleRead)
	s.mux.HandleFunc("PUT /s/{id}/{seq}", s.handleAppend)
	s.mux.HandleFunc("DELETE /s/{id}", s.handleDelete)
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
	})
	if s.cfg.Assets != nil {
		assets := http.FileServer(http.FS(s.cfg.Assets))
		s.mux.Handle("GET /assets/", assets)
		// Only the viewer route falls back to the app shell. Every
		// other path 404s, so the dashboard routes bundled in the same
		// build are unreachable against a backend that cannot serve
		// them.
		s.mux.HandleFunc("GET /v/{id}", s.handleViewer)
		s.mux.HandleFunc("GET /favicon.ico", assets.ServeHTTP)
		s.mux.HandleFunc("GET /robots.txt", assets.ServeHTTP)
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// createResponse is returned when a share is created. The limits are
// echoed so the writer can size and truncate chunks without a separate
// capability call.
type createResponse struct {
	ID            string `json:"id"`
	DeleteToken   string `json:"deleteToken"`
	MaxChunkBytes int64  `json:"maxChunkBytes"`
	MaxChunks     int    `json:"maxChunks"`
	MaxShareBytes int64  `json:"maxShareBytes"`
	ExpiresAt     int64  `json:"expiresAt"`
}

// handleCreate mints an empty share and returns its id and delete token.
// Unauthenticated by design: baking an upload credential into a
// distributed binary would publish it. Abuse is bounded by the rate
// limit here and the size caps on append.
func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	if !s.creates.allow(s.clientKey(r)) {
		http.Error(w, "too many shares created; try again later", http.StatusTooManyRequests)
		return
	}
	now := s.cfg.Now()
	id, err := newID(now)
	if err != nil {
		serverError(w, err)
		return
	}
	token, hash, err := newDeleteToken()
	if err != nil {
		serverError(w, err)
		return
	}
	m := meta{Version: metaVersion, DeleteHash: hash, CreatedAt: now.UnixMilli()}
	if err := putMeta(r.Context(), s.cfg.Store, id, m); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, createResponse{
		ID:            id,
		DeleteToken:   token,
		MaxChunkBytes: s.cfg.MaxChunkBytes,
		MaxChunks:     s.cfg.MaxChunks,
		MaxShareBytes: s.cfg.MaxShareBytes,
		ExpiresAt:     now.Add(s.cfg.TTL).UnixMilli(),
	})
}

// handleAppend stores one sealed chunk at a writer-chosen sequence
// number. Re-uploading the same chunk is a byte-identical overwrite, so
// a client may retry a failed request safely.
func (s *Server) handleAppend(w http.ResponseWriter, r *http.Request) {
	s.mutations.Lock()
	defer s.mutations.Unlock()

	id := r.PathValue("id")
	if _, ok := s.authorise(w, r, id); !ok {
		return
	}
	seq, err := strconv.ParseUint(r.PathValue("seq"), 10, 64)
	if err != nil || seq > share.MaxSeq || seq >= uint64(s.cfg.MaxChunks) {
		// Bounding seq is what keeps chunk accounting meaningful: a
		// client must not be able to scatter chunks across the key
		// space to defeat the per-share chunk cap.
		http.Error(w, "invalid sequence number", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MaxChunkBytes))
	if err != nil {
		http.Error(w, "chunk too large", http.StatusRequestEntityTooLarge)
		return
	}

	prefix, _ := prefixFor(id)
	objs, err := s.cfg.Store.List(r.Context(), prefix)
	if err != nil {
		serverError(w, err)
		return
	}
	key, _ := chunkKey(id, seq)

	var (
		total   int64
		chunks  int
		replace int64
		exists  bool
	)
	for _, o := range objs {
		// Only chunks count against the byte cap. The metadata object
		// is fixed-size overhead the caller cannot control, and
		// charging for it would make small caps unusable.
		if _, isChunk := seqFromKey(o.Key); !isChunk {
			continue
		}
		total += o.Size
		chunks++
		if o.Key == key {
			replace = o.Size
			exists = true
		}
	}
	if !exists && chunks >= s.cfg.MaxChunks {
		http.Error(w, "share has too many chunks", http.StatusRequestEntityTooLarge)
		return
	}
	if total-replace+int64(len(body)) > s.cfg.MaxShareBytes {
		http.Error(w, "share is too large", http.StatusRequestEntityTooLarge)
		return
	}

	if err := s.cfg.Store.Put(r.Context(), key, body); err != nil {
		serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// chunkView is one sealed chunk on the wire. Data is base64 because the
// response carries many chunks in one JSON body; the viewer decodes it
// straight into an ArrayBuffer for WebCrypto.
type chunkView struct {
	Seq  uint64 `json:"seq"`
	Data string `json:"data"`
}

type readResponse struct {
	Chunks []chunkView `json:"chunks"`
	// Last is the highest sequence number returned, or -1 when the
	// response is empty. The viewer polls with from=Last+1.
	Last int64 `json:"last"`
}

// handleRead returns the sealed chunks of a share from a sequence number
// onward. Unauthenticated: the id and the key in the caller's URL
// fragment are the only credentials, and the relay never learns the key.
func (s *Server) handleRead(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Allow any origin: this is public ciphertext, and an ocman
	// instance on another origin fetches it to fork a conversation.
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if !validID(id) {
		http.Error(w, "share not found", http.StatusNotFound)
		return
	}
	_, ok, err := getMeta(r.Context(), s.cfg.Store, id)
	if err != nil {
		serverError(w, err)
		return
	}
	if !ok {
		// Unknown, revoked, and expired all collapse to 404 so a
		// revoked share is indistinguishable from one that never
		// existed.
		http.Error(w, "share not found", http.StatusNotFound)
		return
	}

	var from uint64
	if v := r.URL.Query().Get("from"); v != "" {
		parsed, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			http.Error(w, "invalid from parameter", http.StatusBadRequest)
			return
		}
		from = parsed
	}

	prefix, _ := prefixFor(id)
	objs, err := s.cfg.Store.List(r.Context(), prefix)
	if err != nil {
		serverError(w, err)
		return
	}
	seqs := make([]uint64, 0, len(objs))
	for _, o := range objs {
		seq, isChunk := seqFromKey(o.Key)
		if !isChunk || seq < from {
			continue
		}
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })

	resp := readResponse{Chunks: []chunkView{}, Last: -1}
	for _, seq := range seqs {
		key, _ := chunkKey(id, seq)
		data, err := s.cfg.Store.Get(r.Context(), key)
		if errors.Is(err, share.ErrNotFound) {
			// Deleted between the list and the read; treat the log as
			// ending here rather than serving a hole.
			break
		}
		if err != nil {
			serverError(w, err)
			return
		}
		resp.Chunks = append(resp.Chunks, chunkView{Seq: seq, Data: base64.RawStdEncoding.EncodeToString(data)})
		resp.Last = int64(seq)
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleDelete revokes a share, removing every chunk and its metadata.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	s.mutations.Lock()
	defer s.mutations.Unlock()

	id := r.PathValue("id")
	if _, ok := s.authorise(w, r, id); !ok {
		return
	}
	prefix, _ := prefixFor(id)
	if err := s.cfg.Store.DeletePrefix(r.Context(), prefix); err != nil {
		serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleViewer serves the single-page app shell for a share URL. The
// decryption key lives in the fragment, so the relay receives only the
// id here.
func (s *Server) handleViewer(w http.ResponseWriter, r *http.Request) {
	if !validID(r.PathValue("id")) {
		http.Error(w, "share not found", http.StatusNotFound)
		return
	}
	index, err := fs.ReadFile(s.cfg.Assets, "index.html")
	if err != nil {
		http.Error(w, "viewer is not available", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(index)
}

// authorise resolves a share and checks the bearer delete token. Every
// failure — malformed id, unknown share, wrong token — returns 404, so a
// caller cannot probe for the existence of a share it cannot write.
func (s *Server) authorise(w http.ResponseWriter, r *http.Request, id string) (meta, bool) {
	if !validID(id) {
		http.Error(w, "share not found", http.StatusNotFound)
		return meta{}, false
	}
	m, found, err := getMeta(r.Context(), s.cfg.Store, id)
	if err != nil {
		serverError(w, err)
		return meta{}, false
	}
	if !found || !m.authorises(bearerToken(r)) {
		http.Error(w, "share not found", http.StatusNotFound)
		return meta{}, false
	}
	return m, true
}

// bearerToken extracts the token from an Authorization header.
func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	v := r.Header.Get("Authorization")
	if len(v) <= len(prefix) || !strings.EqualFold(v[:len(prefix)], prefix) {
		return ""
	}
	return v[len(prefix):]
}

// clientKey identifies a client for rate limiting.
func (s *Server) clientKey(r *http.Request) string {
	if s.cfg.TrustProxy {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			first, _, _ := strings.Cut(fwd, ",")
			if ip := strings.TrimSpace(first); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// serverError hides the underlying cause from the client. The relay
// holds other people's ciphertext; storage paths and backend errors are
// not the caller's business.
func serverError(w http.ResponseWriter, _ error) {
	http.Error(w, "internal error", http.StatusInternalServerError)
}
