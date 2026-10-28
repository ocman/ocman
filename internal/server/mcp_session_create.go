package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ocman/ocman/internal/analyticsdata"
	"github.com/ocman/ocman/internal/git"
	"github.com/ocman/ocman/internal/hostsvc"
	internalmcp "github.com/ocman/ocman/internal/mcp"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessionsvc"
)

// CreateSession uses the same defaults and owner-local launch paths as the
// new-conversation composer. Explicit options win over defaults.
func (s sessionMCPService) CreateSession(ctx context.Context, in internalmcp.CreateSessionRequest) (internalmcp.CreatedSession, error) {
	platformID, err := platforms.PlatformForModel(in.Platform, in.Model)
	if err != nil {
		return internalmcp.CreatedSession{}, fmt.Errorf("%w: %s", internalmcp.ErrInvalidSessionCreate, err.Error())
	}
	dir := in.Directory
	var caller *platforms.SessionDetail
	if dir == "" || (platformID == "" && in.SessionID != "") {
		detail, err := s.GetSession(ctx, in.Platform, in.SessionID, 1)
		if err != nil {
			return internalmcp.CreatedSession{}, err
		}
		caller = detail
		if dir == "" {
			dir = projectRootForDirectory(detail.Session.Directory)
		}
		if platformID == "" {
			platformID = detail.Session.Platform
		}
	}
	if platformID == "" {
		var err error
		platformID, err = s.server.stateDB.DefaultPlatform(ctx)
		if err != nil {
			return internalmcp.CreatedSession{}, err
		}
	}
	if !s.server.sessions.KnownPlatform(platformID) {
		return internalmcp.CreatedSession{}, fmt.Errorf("%w: unknown platform", internalmcp.ErrInvalidSessionCreate)
	}
	remoteID, base := platforms.SplitPlatformID(platformID)
	owner := remoteIDForPlatform(platformID)
	host, ok := s.server.router().LookupRemote(owner)
	if !ok {
		return internalmcp.CreatedSession{}, fmt.Errorf("%w: remote %s is not connected", internalmcp.ErrInvalidSessionCreate, owner)
	}
	if in.Model == "" && in.SessionID != "" {
		if caller == nil {
			caller, err = s.GetSession(ctx, in.Platform, in.SessionID, 1)
			if err != nil {
				return internalmcp.CreatedSession{}, err
			}
		}
		in.Model = mcpCallerModel(caller)
	}
	if in.Model == "" {
		// The project default only applies when it runs on this platform.
		if def := s.server.projectDefaultModel(ctx, dir); platforms.ModelPlatform(def) == platforms.ID(base) {
			in.Model = def
		}
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
			Platform: base, ProjectDir: dir, AutoName: true, Prompt: in.Prompt, Title: in.Title,
		})
		if err != nil {
			return internalmcp.CreatedSession{}, err
		}
		created.SessionID, created.Directory = res.SessionID, res.WorktreePath
	} else {
		var port string
		ensured, err := host.EnsureRuntime(ctx, base, projectRootForDirectory(dir))
		switch {
		case err != nil && (remoteID != "" || base != string(platforms.OpenCodeID)):
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

func mcpCallerModel(detail *platforms.SessionDetail) string {
	for _, message := range detail.Messages {
		var data analyticsdata.MessageData
		if json.Unmarshal(message.Data, &data) == nil {
			provider, model := analyticsdata.ModelProvider(data)
			if provider != "" && model != "" {
				return analyticsdata.ModelKey(provider, model)
			}
		}
	}
	return detail.DefaultModel
}

// Match the composer's eligibility rules, including manually created worktrees.
func mcpWorktreeEligible(ctx context.Context, host hostsvc.Host, dir string) (bool, error) {
	if !host.Capabilities().OpencodeLaunch {
		return false, nil
	}
	trees, err := host.ListWorktrees(ctx, dir)
	if errors.Is(err, git.ErrNotARepo) {
		return false, nil
	}
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
