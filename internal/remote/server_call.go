package remote

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type callHandler func(*Server, context.Context, *pb.CallReq) (*pb.JsonResp, error)

// callHandlers is the closed set of unary operations served by Call.
var callHandlers = map[string]callHandler{
	"SessionLifecycle":        (*Server).SessionLifecycle,
	"SessionSummary":          (*Server).SessionSummary,
	"SessionChanges":          (*Server).SessionChanges,
	"SessionInfo":             (*Server).SessionInfo,
	"AgentCatalog":            (*Server).AgentCatalog,
	"SlashCommands":           (*Server).SlashCommands,
	"SessionModels":           (*Server).SessionModels,
	"DirectoryCatalog":        (*Server).DirectoryCatalog,
	"ListPermissions":         (*Server).ListPermissions,
	"RefreshPermissions":      (*Server).RefreshPermissions,
	"ListQuestions":           (*Server).ListQuestions,
	"PermissionRules":         (*Server).PermissionRules,
	"SessionPermissionPolicy": (*Server).SessionPermissionPolicy,
	"Capabilities":            (*Server).Capabilities,
	"NativeQueued":            (*Server).NativeQueued,
	"SessionUsage":            (*Server).SessionUsage,
	"SendMessage":             (*Server).SendMessage,
	"ExecuteCommand":          (*Server).ExecuteCommand,
	"RunShell":                (*Server).RunShell,
	"RespondPermission":       (*Server).RespondPermission,
	"RespondQuestion":         (*Server).RespondQuestion,
	"RejectQuestion":          (*Server).RejectQuestion,
	"Abort":                   (*Server).Abort,
	"RenameSession":           (*Server).RenameSession,
	"SetPermissionRules":      (*Server).SetPermissionRules,
	"Compact":                 (*Server).Compact,
	"ForkSession":             (*Server).ForkSession,
	"MoveSession":             (*Server).MoveSession,
	"CreateSession":           (*Server).CreateSession,
	"CancelNativeQueued":      (*Server).CancelNativeQueued,
	"DisposeSession":          (*Server).DisposeSession,
	"SessionIdentity":         (*Server).SessionIdentity,
	"SessionIdentities":       (*Server).SessionIdentities,
	"GitInfo":                 (*Server).GitInfo,
	"GitDiff":                 (*Server).GitDiff,
	"GitBranches":             (*Server).GitBranches,
	"GitCheckout":             (*Server).GitCheckout,
	"ListRepoFiles":           (*Server).ListRepoFiles,
	"ReadRepoFile":            (*Server).ReadRepoFile,
	"ProjectUpstreams":        (*Server).ProjectUpstreams,
	"FetchPRHead":             (*Server).FetchPRHead,
	"ListWorktrees":           (*Server).ListWorktrees,
	"WorktreeDefaultBaseRef":  (*Server).WorktreeDefaultBaseRef,
	"CreateWorktreeSession":   (*Server).CreateWorktreeSession,
	"RemoveWorktree":          (*Server).RemoveWorktree,
	"LaunchTmux":              (*Server).LaunchTmux,
	"EnsureProjectOpencode":   (*Server).EnsureProjectOpencode,
	"EnsureRuntime":           (*Server).EnsureRuntime,
	"StopProjectOpencode":     (*Server).StopProjectOpencode,
	"RestartProjectOpencode":  (*Server).RestartProjectOpencode,
	"ReloadOpencode":          (*Server).ReloadOpencode,
	"ManagedOpencodes":        (*Server).ManagedOpencodes,
	"TmuxSessions":            (*Server).TmuxSessions,
	"HostCapabilities":        (*Server).HostCapabilities,
	"InboxItems":              (*Server).InboxItems,
	"ArchivedInboxItems":      (*Server).ArchivedInboxItems,
	"MarkInboxItemRead":       (*Server).MarkInboxItemRead,
	"MarkInboxItemUnread":     (*Server).MarkInboxItemUnread,
	"ArchiveInboxItems":       (*Server).ArchiveInboxItems,
	"PluginOperation":         (*Server).PluginOperation,
	"TermWindows":             (*Server).TermWindows,
	"TermCreateWindow":        (*Server).TermCreateWindow,
	"TermKillWindow":          (*Server).TermKillWindow,
	"Projects":                (*Server).Projects,
}

// Call dispatches a unary operation. An unknown method answers
// Unimplemented, as a missing typed RPC did, so client fallbacks still apply.
func (s *Server) Call(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	handler, ok := callHandlers[req.Method]
	if !ok {
		return nil, status.Errorf(codes.Unimplemented, "unknown remote method %q", req.Method)
	}
	return handler(s, ctx, req)
}

// call runs one unary operation on the owner, failing closed with
// ErrRemoteOffline while disconnected. A non-nil args is sent as the JSON
// payload.
func (c *RemoteConn) call(ctx context.Context, req *pb.CallReq, args any, opts ...grpc.CallOption) ([]byte, error) {
	client := c.Client()
	if client == nil {
		return nil, platforms.ErrRemoteOffline
	}
	if args != nil {
		payload, err := marshalJSON(args)
		if err != nil {
			return nil, err
		}
		req.Payload = payload
	}
	resp, err := client.Call(ctx, req, opts...)
	return resp.GetPayload(), err
}
