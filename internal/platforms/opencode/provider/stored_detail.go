package opencode

import (
	"context"
	"sync"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (a *Adapter) ChangesFromStored(ctx context.Context, detail *platforms.SessionDetail) (*platforms.SessionChanges, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if detail == nil || detail.Session == nil {
		return nil, platforms.ErrNotFound
	}
	return aggregateChanges(detail.Session.ID, detail.Session.Directory, detail.Parts), nil
}

func (a *Adapter) InfoFromStored(ctx context.Context, detail *platforms.SessionDetail) (*platforms.SessionInfo, error) {
	if detail == nil || detail.Session == nil {
		return nil, platforms.ErrNotFound
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	tier := computeAlwaysOnTier(detail.Messages, detail.Parts, a.pricing)
	port := ""
	if detail.Session.LiveConnection {
		port = a.sessionRuntimePort(detail.Session.ID)
	}
	var mcp map[string]rawMCPEntry
	var lsp []rawLSPEntry
	var providers OpenCodeProvidersResponse
	var hasProviders bool
	if port != "" {
		var wg sync.WaitGroup
		wg.Go(func() { mcp = fetchOpenCodeMCP(port) })
		wg.Go(func() { lsp = fetchOpenCodeLSP(port) })
		wg.Go(func() { providers, hasProviders = fetchOpenCodeProviders(ctx, port, detail.Session.Directory) })
		wg.Wait()
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var providerConfig *OpenCodeProvidersResponse
	if hasProviders {
		providerConfig = &providers
	}
	info := buildSessionInfo(detail.Session.ID, tier.ctxTokens, tier.cost, tier.estCost, detail.DefaultModel, mcp, lsp, providerConfig)
	info.Supported = port != ""
	info.Tokens, info.Messages, info.Todos = tier.tokens, tier.messages, tier.todos
	return info, nil
}
