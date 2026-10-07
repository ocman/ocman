package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
)

// CreateSession uses the same defaults and owner-local launch paths as the
// new-conversation composer. Explicit options win over defaults.
func (s sessionMCPService) CreateSession(ctx context.Context, in internalmcp.CreateSessionRequest) (internalmcp.CreatedSession, error) {
	platformID, dir := in.Platform, in.Directory
	if dir == "" {
		detail, err := s.GetSession(ctx, in.Platform, in.SessionID, 0)
		if err != nil {
			return internalmcp.CreatedSession{}, err
		}
		dir = projectRootForDirectory(detail.Session.Directory)
	}
	if platformID == "" {
		platformID = string(opencode.PlatformID)
	}
	if !s.server.sessions.KnownPlatform(platformID) {
		return internalmcp.CreatedSession{}, fmt.Errorf("%w: unknown platform", internalmcp.ErrInvalidSessionCreate)
	}
	remoteID, _ := remote.SplitPlatformID(platformID)
	owner := remoteIDForPlatform(platformID)
	host, ok := s.server.router().LookupRemote(owner)
	if !ok {
		return internalmcp.CreatedSession{}, fmt.Errorf("%w: remote %s is not connected", internalmcp.ErrInvalidSessionCreate, owner)
	}
	if in.Model == "" {
		in.Model = s.server.projectDefaultModel(ctx, dir)
	}
	if in.Agent == "" && s.server.stateDB != nil {
		var err error
		in.Agent, err = s.server.defaultAgent(ctx)
		if err != nil {
			return internalmcp.CreatedSession{}, err
		}
	}
	var worktree bool
	if in.Worktree != nil {
		worktree = *in.Worktree
	} else {
		var err error
		worktree, err = mcpWorktreeEligible(ctx, host, dir)
		if err != nil {
			return internalmcp.CreatedSession{}, err
		}
	}
	created := internalmcp.CreatedSession{Platform: platformID, Directory: dir}
	if worktree {
		res, err := host.CreateWorktreeSession(ctx, hostsvc.WorktreeSessionRequest{
			ProjectDir: dir, AutoName: true, Prompt: in.Prompt, Title: in.Title,
		})
		if err != nil {
			return internalmcp.CreatedSession{}, err
		}
		created.SessionID, created.Directory = res.SessionID, res.WorktreePath
	} else {
		var port string
		ensured, err := host.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: projectRootForDirectory(dir)})
		switch {
		case err != nil && remoteID != "":
			return internalmcp.CreatedSession{}, err
		case err == nil:
			port = ensured.Port()
		} // A local ensure failure falls back to port discovery, as in handleCreateSession.
		resp, err := s.server.sessions.Create(ctx, platformID, platforms.CreateSessionRequest{Directory: dir, Title: in.Title, Port: port})
		var ve *sessionsvc.ValidationError
		if errors.As(err, &ve) {
			return internalmcp.CreatedSession{}, fmt.Errorf("%w: %s", internalmcp.ErrInvalidSessionCreate, ve.Error())
		}
		if err != nil {
			return internalmcp.CreatedSession{}, err
		}
		created.SessionID = resp.ID
	}
	if err := s.server.sendNow(ctx, platformID, platforms.SendMessageRequest{SessionID: created.SessionID, Message: in.Prompt, Model: in.Model, Agent: in.Agent}); err != nil {
		return created, err
	}
	return created, nil
}

// Match the composer's eligibility rules, including manually created worktrees.
func mcpWorktreeEligible(ctx context.Context, host hostsvc.Host, dir string) (bool, error) {
	if !host.Capabilities().OpencodeLaunch {
		return false, nil
	}
	info, err := host.GitInfo(ctx, []string{dir})
	if err != nil {
		return false, err
	}
	if info[dir].Branch == "" {
		return false, nil
	}
	trees, err := host.ListWorktrees(ctx, dir)
	if err != nil {
		return false, err
	}
	for _, tree := range trees {
		if !tree.Main && (dir == tree.Path || strings.HasPrefix(dir, tree.Path+"/")) {
			return false, nil
		}
	}
	base, err := host.WorktreeDefaultBaseRef(ctx, dir)
	return base != "", err
}
