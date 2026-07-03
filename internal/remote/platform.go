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
	nameFn func() string

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
	return platforms.ID(CompoundPlatformID(p.remoteID(), p.base))
}

func (p *remotePlatform) DisplayName() string {
	// Display name is the platform's, not the host's; the host badge is
	// rendered separately from RemoteName.
	if p.base == "opencode" {
		return "OpenCode"
	}
	return p.base
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
	client := p.conn.Client()
	if client == nil {
		return platforms.Capabilities{}
	}
	resp, err := client.Capabilities(context.Background(), &pb.PlatformRef{Platform: p.base})
	if err != nil {
		return platforms.Capabilities{}
	}
	var caps platforms.Capabilities
	if err := unmarshalJSON(resp.Payload, &caps); err != nil {
		return platforms.Capabilities{}
	}
	p.mu.Lock()
	p.caps = caps
	p.capsSet = true
	p.mu.Unlock()
	return caps
}

// stamp annotates sessions with host identity + ID, recording ownership.
func (p *remotePlatform) stamp(sessions []db.Session, stale bool) []db.Session {
	rid := p.remoteID()
	name := p.nameFn()
	owned := make(map[string]struct{}, len(sessions))
	for i := range sessions {
		sessions[i].Platform = string(p.ID())
		sessions[i].RemoteID = rid
		sessions[i].RemoteName = name
		sessions[i].Stale = stale
		owned[sessions[i].ID] = struct{}{}
	}
	if !stale {
		p.mu.Lock()
		p.owned = owned
		p.lastSess = sessions
		p.mu.Unlock()
	}
	return sessions
}

func (p *remotePlatform) Sessions(ctx context.Context, dir string, since int64) ([]db.Session, error) {
	client := p.conn.Client()
	if client == nil {
		return p.staleSessions(), nil
	}
	resp, err := client.Sessions(ctx, &pb.SessionsReq{Platform: p.base, Dir: dir, Since: since})
	if err != nil {
		p.conn.markOffline()
		return p.staleSessions(), nil
	}
	p.conn.markSeen()
	var sessions []db.Session
	if err := unmarshalJSON(resp.Payload, &sessions); err != nil {
		return nil, err
	}
	return p.stamp(sessions, false), nil
}

// staleSessions returns the last-known sessions flagged stale (Phase 5).
func (p *remotePlatform) staleSessions() []db.Session {
	p.mu.RLock()
	last := p.lastSess
	p.mu.RUnlock()
	out := make([]db.Session, len(last))
	copy(out, last)
	for i := range out {
		out[i].Stale = true
	}
	return out
}

func (p *remotePlatform) Session(ctx context.Context, id string, limit, offset int) (*platforms.SessionDetail, error) {
	client := p.conn.Client()
	if client == nil {
		return nil, ErrRemoteOffline
	}
	resp, err := client.Session(ctx, &pb.SessionReq{Platform: p.base, SessionId: id, Limit: int32(limit), Offset: int32(offset)})
	if err != nil {
		p.conn.markOffline()
		return nil, err
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
		detail.Session.LiveConnection = true
	}
	return &detail, nil
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
	return out, nil
}

func (p *remotePlatform) SessionChanges(ctx context.Context, sessionID string) (*platforms.SessionChanges, error) {
	return jsonCall(ctx, p, func(c pb.OcmanClient) (*pb.JsonResp, error) {
		return c.SessionChanges(ctx, &pb.SessionRef{Platform: p.base, SessionId: sessionID})
	}, &platforms.SessionChanges{})
}

func (p *remotePlatform) SessionInfo(ctx context.Context, sessionID string) (*platforms.SessionInfo, error) {
	return jsonCall(ctx, p, func(c pb.OcmanClient) (*pb.JsonResp, error) {
		return c.SessionInfo(ctx, &pb.SessionRef{Platform: p.base, SessionId: sessionID})
	}, &platforms.SessionInfo{})
}

// LiveStatus is in-memory/local-only; remote live status is reflected
// through the session list and event stream, so the hub-side adapter
// returns nil.
func (p *remotePlatform) LiveStatus(string) *platforms.LiveState { return nil }

func (p *remotePlatform) AgentCatalog(ctx context.Context, sessionID string) ([]platforms.AgentCatalogEntry, error) {
	client := p.conn.Client()
	if client == nil {
		return nil, ErrRemoteOffline
	}
	resp, err := client.AgentCatalog(ctx, &pb.SessionRef{Platform: p.base, SessionId: sessionID})
	if err != nil {
		return nil, err
	}
	var out []platforms.AgentCatalogEntry
	return out, unmarshalJSON(resp.Payload, &out)
}

func (p *remotePlatform) SlashCommands(ctx context.Context, sessionID string) ([]platforms.SlashCommandEntry, error) {
	client := p.conn.Client()
	if client == nil {
		return nil, ErrRemoteOffline
	}
	resp, err := client.SlashCommands(ctx, &pb.SessionRef{Platform: p.base, SessionId: sessionID})
	if err != nil {
		return nil, err
	}
	var out []platforms.SlashCommandEntry
	return out, unmarshalJSON(resp.Payload, &out)
}

func (p *remotePlatform) SessionModels(ctx context.Context, sessionID string) (*platforms.SessionModelsResponse, error) {
	return jsonCall(ctx, p, func(c pb.OcmanClient) (*pb.JsonResp, error) {
		return c.SessionModels(ctx, &pb.SessionRef{Platform: p.base, SessionId: sessionID})
	}, &platforms.SessionModelsResponse{})
}

func (p *remotePlatform) ListPermissions(ctx context.Context, sessionID string) ([]platforms.LivePrompt, error) {
	client := p.conn.Client()
	if client == nil {
		return nil, ErrRemoteOffline
	}
	resp, err := client.ListPermissions(ctx, &pb.SessionRef{Platform: p.base, SessionId: sessionID})
	if err != nil {
		return nil, err
	}
	var out []platforms.LivePrompt
	return out, unmarshalJSON(resp.Payload, &out)
}

func (p *remotePlatform) ListQuestions(ctx context.Context, sessionID string) ([]platforms.LivePrompt, error) {
	client := p.conn.Client()
	if client == nil {
		return nil, ErrRemoteOffline
	}
	resp, err := client.ListQuestions(ctx, &pb.SessionRef{Platform: p.base, SessionId: sessionID})
	if err != nil {
		return nil, err
	}
	var out []platforms.LivePrompt
	return out, unmarshalJSON(resp.Payload, &out)
}

// --- mutations ---

func (p *remotePlatform) SendMessage(ctx context.Context, req platforms.SendMessageRequest) error {
	return p.mutate(ctx, req, func(c pb.OcmanClient, b []byte) error {
		_, err := c.SendMessage(ctx, &pb.PlatformJsonReq{Platform: p.base, Payload: b})
		return err
	})
}

func (p *remotePlatform) ExecuteCommand(ctx context.Context, req platforms.ExecuteCommandRequest) error {
	return p.mutate(ctx, req, func(c pb.OcmanClient, b []byte) error {
		_, err := c.ExecuteCommand(ctx, &pb.PlatformJsonReq{Platform: p.base, Payload: b})
		return err
	})
}

func (p *remotePlatform) RunShell(ctx context.Context, req platforms.RunShellRequest) error {
	return p.mutate(ctx, req, func(c pb.OcmanClient, b []byte) error {
		_, err := c.RunShell(ctx, &pb.PlatformJsonReq{Platform: p.base, Payload: b})
		return err
	})
}

func (p *remotePlatform) RespondPermission(ctx context.Context, req platforms.RespondPermissionRequest) error {
	return p.mutate(ctx, req, func(c pb.OcmanClient, b []byte) error {
		_, err := c.RespondPermission(ctx, &pb.PlatformJsonReq{Platform: p.base, Payload: b})
		return err
	})
}

func (p *remotePlatform) RespondQuestion(ctx context.Context, req platforms.RespondQuestionRequest) error {
	return p.mutate(ctx, req, func(c pb.OcmanClient, b []byte) error {
		_, err := c.RespondQuestion(ctx, &pb.PlatformJsonReq{Platform: p.base, Payload: b})
		return err
	})
}

func (p *remotePlatform) RejectQuestion(ctx context.Context, req platforms.RejectQuestionRequest) error {
	return p.mutate(ctx, req, func(c pb.OcmanClient, b []byte) error {
		_, err := c.RejectQuestion(ctx, &pb.PlatformJsonReq{Platform: p.base, Payload: b})
		return err
	})
}

func (p *remotePlatform) Abort(ctx context.Context, req platforms.AbortRequest) error {
	return p.mutate(ctx, req, func(c pb.OcmanClient, b []byte) error {
		_, err := c.Abort(ctx, &pb.PlatformJsonReq{Platform: p.base, Payload: b})
		return err
	})
}

func (p *remotePlatform) RenameSession(ctx context.Context, req platforms.RenameSessionRequest) error {
	return p.mutate(ctx, req, func(c pb.OcmanClient, b []byte) error {
		_, err := c.RenameSession(ctx, &pb.PlatformJsonReq{Platform: p.base, Payload: b})
		return err
	})
}

func (p *remotePlatform) Compact(ctx context.Context, req platforms.CompactRequest) error {
	return p.mutate(ctx, req, func(c pb.OcmanClient, b []byte) error {
		_, err := c.Compact(ctx, &pb.PlatformJsonReq{Platform: p.base, Payload: b})
		return err
	})
}

func (p *remotePlatform) CreateSession(ctx context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	client := p.conn.Client()
	if client == nil {
		return nil, ErrRemoteOffline
	}
	b, err := marshalJSON(req)
	if err != nil {
		return nil, err
	}
	resp, err := client.CreateSession(ctx, &pb.PlatformJsonReq{Platform: p.base, Payload: b})
	if err != nil {
		return nil, remotePlatformError(err)
	}
	var out platforms.CreateSessionResponse
	return &out, unmarshalJSON(resp.Payload, &out)
}

func (p *remotePlatform) ProxyEvents(ctx context.Context, sessionID string, w io.Writer, flush func()) error {
	client := p.conn.Client()
	if client == nil {
		return ErrRemoteOffline
	}
	stream, err := client.StreamEvents(ctx, &pb.SessionRef{Platform: p.base, SessionId: sessionID})
	if err != nil {
		return err
	}
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := w.Write(chunk.Data); err != nil {
			return err
		}
		flush()
	}
}

// mutate marshals req and runs fn against the live client.
func (p *remotePlatform) mutate(_ context.Context, req any, fn func(pb.OcmanClient, []byte) error) error {
	client := p.conn.Client()
	if client == nil {
		return ErrRemoteOffline
	}
	b, err := marshalJSON(req)
	if err != nil {
		return err
	}
	return remotePlatformError(fn(client, b))
}

// jsonCall runs a unary JsonResp RPC and decodes into dst.
func jsonCall[T any](_ context.Context, p *remotePlatform, fn func(pb.OcmanClient) (*pb.JsonResp, error), dst *T) (*T, error) {
	client := p.conn.Client()
	if client == nil {
		return nil, ErrRemoteOffline
	}
	resp, err := fn(client)
	if err != nil {
		return nil, remotePlatformError(err)
	}
	if err := unmarshalJSON(resp.Payload, dst); err != nil {
		return nil, err
	}
	return dst, nil
}

func remotePlatformError(err error) error {
	if err == nil {
		return nil
	}
	if status.Code(err) == codes.Unavailable {
		return errors.Join(platforms.ErrPlatformUnreachable, err)
	}
	return err
}
