package remote

import (
	"context"
	"errors"
	"io"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
)

// remotePlatform implements platforms.Platform by translating every call
// into a gRPC RPC to the owning remote (AD-1). Its ID() is the compound
// "r-<remoteID>:<base>" key (AD-2); it stamps RemoteID/RemoteName/Stale
// onto returned sessions (AD-7) and keeps a cache-only ownership map
// (AD-2b) so Owns never makes a gRPC call.
type remotePlatform struct {
	conn *RemoteConn
	base string // base platform id, e.g. "opencode"

	// nameFn returns the current display name for the remote (the
	// hub-assigned label, or hostname when unnamed). Looked up live so a
	// rename takes effect without re-registering the adapter.
	nameFn          func() string
	pluginReference func(context.Context, string) (string, error)

	mu       sync.RWMutex
	caps     platforms.Capabilities
	capsSet  bool
	owned    map[string]struct{} // session IDs last seen owned by this remote
	lastSess []db.Session        // last-known sessions for offline stale serving (Phase 5)
}

// newRemotePlatform builds an adapter for a remote's base platform.
func newRemotePlatform(conn *RemoteConn, base string, nameFn func() string) *remotePlatform {
	return &remotePlatform{
		conn:   conn,
		base:   base,
		nameFn: nameFn,
		owned:  make(map[string]struct{}),
	}
}

func (p *remotePlatform) remoteID() string { return p.conn.RemoteID() }

func (p *remotePlatform) ID() platforms.ID {
	return platforms.ID(platforms.CompoundPlatformID(p.remoteID(), p.base))
}

func (p *remotePlatform) DisplayName() string {
	// Display name is the platform's, not the host's; the host badge is
	// rendered separately from RemoteName.
	return platforms.DisplayName(platforms.ID(p.base))
}

func (p *remotePlatform) Available(_ context.Context) bool {
	return p.conn.Health() == HealthConnected
}

func (p *remotePlatform) Capabilities() platforms.Capabilities {
	p.mu.RLock()
	if p.capsSet {
		defer p.mu.RUnlock()
		return p.caps
	}
	p.mu.RUnlock()
	// Fetch lazily and cache.
	payload, err := p.call(context.Background(), "Capabilities", "", nil)
	if err != nil {
		return platforms.Capabilities{}
	}
	var caps platforms.Capabilities
	if err := unmarshalJSON(payload, &caps); err != nil {
		return platforms.Capabilities{}
	}
	if p.base == string(platforms.OpenCodeID) {
		// Remotes predating these flags always supported both on OpenCode.
		caps.Revert, caps.RuntimeRestart, caps.ForkAtMessage = true, true, true
	}
	p.mu.Lock()
	p.caps = caps
	p.capsSet = true
	p.mu.Unlock()
	return caps
}

func (p *remotePlatform) Sessions(ctx context.Context, dir string, since int64) ([]db.Session, error) {
	client := p.conn.Client()
	if client == nil {
		return p.checkedStaleSessions(dir, since)
	}
	resp, err := client.Sessions(ctx, &pb.SessionsReq{Platform: p.base, Dir: dir, Since: since})
	if err != nil {
		p.conn.markOffline()
		return p.checkedStaleSessions(dir, since)
	}
	p.conn.markSeen()
	var sessions []db.Session
	if err := unmarshalJSON(resp.Payload, &sessions); err != nil {
		return nil, err
	}
	return p.checkedSessions(sessions, dir, since)
}

func (p *remotePlatform) Session(ctx context.Context, id string, limit, offset int) (*platforms.SessionDetail, error) {
	client := p.conn.Client()
	if client == nil {
		return nil, platforms.ErrRemoteOffline
	}
	resp, err := client.Session(ctx, &pb.SessionReq{Platform: p.base, SessionId: id, Limit: int32(limit), Offset: int32(offset)})
	if err != nil {
		return nil, conversationReadError(p.conn, err)
	}
	p.conn.markSeen()
	var detail platforms.SessionDetail
	if err := unmarshalJSON(resp.Payload, &detail); err != nil {
		return nil, err
	}
	if detail.Session != nil {
		detail.Session.Platform = string(p.ID())
		detail.Session.RemoteID = p.remoteID()
		detail.Session.RemoteName = p.nameFn()
		detail.Session.LiveConnection = p.live(detail.Session.LiveConnection)
	}
	for i := range detail.SessionTree {
		detail.SessionTree[i].Platform = string(p.ID())
		detail.SessionTree[i].RemoteID = p.remoteID()
		detail.SessionTree[i].RemoteName = p.nameFn()
		detail.SessionTree[i].LiveConnection = p.live(detail.SessionTree[i].LiveConnection)
	}
	return p.checkedDetail(&detail, id)
}

// live reports a remote session's write availability. A connected remote
// OpenCode is always drivable (the owner launches its instance on send);
// a provider reports per session, so a terminal-started Claude session stays
// read-only from the hub too (FR-4).
func (p *remotePlatform) live(owner bool) bool {
	return owner || p.base == string(platforms.OpenCodeID)
}

// Owns is cache-only: it consults the ownership map populated by
// Sessions() and never makes a gRPC call (AD-2b).
func (p *remotePlatform) Owns(_ context.Context, sessionID string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.owned[sessionID]
	return ok
}

func (p *remotePlatform) SessionsInactiveBefore(ctx context.Context, cutoff int64) ([]db.SessionArchiveCandidate, error) {
	client := p.conn.Client()
	if client == nil {
		return nil, nil
	}
	resp, err := client.SessionsInactiveBefore(ctx, &pb.CutoffReq{Platform: p.base, Cutoff: cutoff})
	if err != nil {
		return nil, nil // best-effort; archive must not block on a remote
	}
	var out []db.SessionArchiveCandidate
	if err := unmarshalJSON(resp.Payload, &out); err != nil {
		return nil, err
	}
	return p.checkedArchive(out)
}

func (p *remotePlatform) SessionChanges(ctx context.Context, sessionID string) (*platforms.SessionChanges, error) {
	return jsonCall(ctx, p, "SessionChanges", sessionID, nil, &platforms.SessionChanges{})
}

func (p *remotePlatform) SessionInfo(ctx context.Context, sessionID string) (*platforms.SessionInfo, error) {
	return jsonCall(ctx, p, "SessionInfo", sessionID, nil, &platforms.SessionInfo{})
}

// LiveStatus is in-memory/local-only; remote live status is reflected
// through the session list and event stream, so the hub-side adapter
// returns nil.
func (p *remotePlatform) LiveStatus(string) *platforms.LiveState { return nil }

func (p *remotePlatform) AgentCatalog(ctx context.Context, sessionID string) ([]platforms.AgentCatalogEntry, error) {
	return sliceCall[platforms.AgentCatalogEntry](ctx, p, "AgentCatalog", sessionID)
}

func (p *remotePlatform) SlashCommands(ctx context.Context, sessionID string) ([]platforms.SlashCommandEntry, error) {
	return sliceCall[platforms.SlashCommandEntry](ctx, p, "SlashCommands", sessionID)
}

func (p *remotePlatform) SessionModels(ctx context.Context, sessionID string) (*platforms.SessionModelsResponse, error) {
	return jsonCall(ctx, p, "SessionModels", sessionID, nil, &platforms.SessionModelsResponse{})
}

func (p *remotePlatform) DirectoryCatalog(ctx context.Context, req platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, error) {
	return jsonCall(ctx, p, "DirectoryCatalog", "", req, &platforms.DirectoryCatalog{})
}

func (p *remotePlatform) ListPermissions(ctx context.Context, sessionID string) ([]platforms.LivePrompt, error) {
	return sliceCall[platforms.LivePrompt](ctx, p, "ListPermissions", sessionID)
}

// RefreshPermissions asks the owner for an authoritative permission list.
func (p *remotePlatform) RefreshPermissions(ctx context.Context, sessionID string) ([]platforms.LivePrompt, error) {
	return sliceCall[platforms.LivePrompt](ctx, p, "RefreshPermissions", sessionID)
}

func (p *remotePlatform) ListQuestions(ctx context.Context, sessionID string) ([]platforms.LivePrompt, error) {
	return sliceCall[platforms.LivePrompt](ctx, p, "ListQuestions", sessionID)
}

func (p *remotePlatform) PermissionRules(ctx context.Context, sessionID string) ([]platforms.PermissionRule, error) {
	return sliceCall[platforms.PermissionRule](ctx, p, "PermissionRules", sessionID)
}

// --- mutations ---

func (p *remotePlatform) SendMessage(ctx context.Context, req platforms.SendMessageRequest) error {
	return p.mutate(ctx, "SendMessage", req)
}

func (p *remotePlatform) ExecuteCommand(ctx context.Context, req platforms.ExecuteCommandRequest) error {
	return p.mutate(ctx, "ExecuteCommand", req)
}

func (p *remotePlatform) RunShell(ctx context.Context, req platforms.RunShellRequest) error {
	return p.mutate(ctx, "RunShell", req)
}

func (p *remotePlatform) RespondPermission(ctx context.Context, req platforms.RespondPermissionRequest) error {
	return p.mutate(ctx, "RespondPermission", req)
}

func (p *remotePlatform) RespondQuestion(ctx context.Context, req platforms.RespondQuestionRequest) error {
	return p.mutate(ctx, "RespondQuestion", req)
}

func (p *remotePlatform) RejectQuestion(ctx context.Context, req platforms.RejectQuestionRequest) error {
	return p.mutate(ctx, "RejectQuestion", req)
}

func (p *remotePlatform) NativeQueued(ctx context.Context, sessionID string) ([]platforms.NativeQueuedMessage, error) {
	return sliceCall[platforms.NativeQueuedMessage](ctx, p, "NativeQueued", sessionID)
}

func (p *remotePlatform) CancelNativeQueued(ctx context.Context, req platforms.CancelNativeQueuedRequest) error {
	return p.mutate(ctx, "CancelNativeQueued", req)
}

func (p *remotePlatform) Abort(ctx context.Context, req platforms.AbortRequest) error {
	if req.TaskID != "" && !p.Capabilities().BackgroundTaskStop {
		return platforms.ErrUnsupported
	}
	return p.mutate(ctx, "Abort", req)
}

func (p *remotePlatform) RenameSession(ctx context.Context, req platforms.RenameSessionRequest) error {
	return p.mutate(ctx, "RenameSession", req)
}

func (p *remotePlatform) SetPermissionRules(ctx context.Context, req platforms.SetPermissionRulesRequest) error {
	if req.NativeMode != nil {
		return p.mutate(ctx, "SetNativePermissionMode", req)
	}
	return p.mutate(ctx, "SetPermissionRules", req)
}

func (p *remotePlatform) Compact(ctx context.Context, req platforms.CompactRequest) error {
	return p.mutate(ctx, "Compact", req)
}

func (p *remotePlatform) MoveSession(ctx context.Context, req platforms.MoveSessionRequest) error {
	return p.mutate(ctx, "MoveSession", req)
}

func (p *remotePlatform) ForkSession(ctx context.Context, req platforms.ForkSessionRequest) (*platforms.CreateSessionResponse, error) {
	payload, err := p.call(ctx, "ForkSession", "", req)
	if err != nil {
		return nil, remotePlatformError(err)
	}
	var out platforms.CreateSessionResponse
	return canonicalDecodeResult(p, payload, &out)
}

func (p *remotePlatform) CreateSession(ctx context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	payload, err := p.call(ctx, "CreateSession", "", req)
	if err != nil {
		return nil, remotePlatformError(err)
	}
	var out platforms.CreateSessionResponse
	return canonicalDecodeResult(p, payload, &out)
}

func (p *remotePlatform) proxySessionEvents(ctx context.Context, sessionID string, w io.Writer, flush func()) error {
	client := p.conn.Client()
	if client == nil {
		return platforms.ErrRemoteOffline
	}
	stream, err := client.StreamEvents(ctx, &pb.SessionRef{Platform: p.base, SessionId: sessionID})
	if err != nil {
		return err
	}
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := w.Write(chunk.Data); err != nil {
			return err
		}
		if flush != nil {
			flush()
		}
	}
}

// call runs one Call operation scoped to this platform.
func (p *remotePlatform) call(ctx context.Context, method, sessionID string, args any) ([]byte, error) {
	return p.conn.call(ctx, &pb.CallReq{Method: method, Platform: p.base, SessionId: sessionID}, args)
}

// mutate sends req as the JSON payload of method.
func (p *remotePlatform) mutate(ctx context.Context, method string, req any) error {
	_, err := p.call(ctx, method, "", req)
	return remotePlatformError(err)
}

// sliceCall runs a session read and decodes the payload into a []T. Like
// jsonCall, RPC errors are mapped through remotePlatformError so
// Unavailable surfaces as ErrPlatformUnreachable.
func sliceCall[T any](ctx context.Context, p *remotePlatform, method, sessionID string) ([]T, error) {
	payload, err := p.call(ctx, method, sessionID, nil)
	if err != nil {
		return nil, remotePlatformError(err)
	}
	var out []T
	if err := unmarshalJSON(payload, &out); err != nil {
		return nil, err
	}
	return canonicalSliceResult(p, out)
}

// jsonCall runs a Call operation and decodes into dst.
func jsonCall[T any](ctx context.Context, p *remotePlatform, method, sessionID string, args any, dst *T) (*T, error) {
	payload, err := p.call(ctx, method, sessionID, args)
	if err != nil {
		return nil, remotePlatformError(err)
	}
	if err := unmarshalJSON(payload, dst); err != nil {
		return nil, err
	}
	return canonicalJSONResult(p, dst)
}

func remotePlatformError(err error) (out error) {
	defer func() {
		if creationWasRejected(err) {
			out = errors.Join(platforms.ErrCreationRejected, out)
		}
	}()
	if err == nil {
		return nil
	}
	switch status.Code(err) {
	case codes.NotFound:
		return errors.Join(platforms.ErrNotFound, err)
	case codes.Unavailable:
		return errors.Join(platforms.ErrPlatformUnreachable, err)
	case codes.Unimplemented: // includes a remote too old to know the RPC
		return errors.Join(platforms.ErrUnsupported, err)
	}
	return err
}
