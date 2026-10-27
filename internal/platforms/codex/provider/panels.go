package codexprovider

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// ChangesFromStored rolls every file-change part of the stored conversation
// into the session changes panel, one entry per file in first-touched order.
// Files whose edits added and removed nothing are left out.
func ChangesFromStored(ctx context.Context, detail *platforms.SessionDetail) (*platforms.SessionChanges, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if detail == nil || detail.Session == nil {
		return nil, platforms.ErrNotFound
	}
	out := &platforms.SessionChanges{SessionID: detail.Session.ID, Supported: true, Files: []platforms.FileChange{}}
	byPath := map[string]*platforms.FileChange{}
	var order []string
	for _, p := range detail.Parts {
		var part struct {
			Tool  string `json:"tool"`
			State struct {
				Metadata struct {
					Files []struct {
						FilePath  string `json:"filePath"`
						Patch     string `json:"patch"`
						Additions int    `json:"additions"`
						Deletions int    `json:"deletions"`
					} `json:"files"`
				} `json:"metadata"`
			} `json:"state"`
		}
		if json.Unmarshal(p.Data, &part) != nil || part.Tool != "apply_patch" {
			continue
		}
		for _, f := range part.State.Metadata.Files {
			fc := byPath[f.FilePath]
			if fc == nil {
				fc = &platforms.FileChange{Path: f.FilePath, DisplayPath: relative(f.FilePath, detail.Session.Directory), FirstEditAt: p.TimeCreated, Edits: []platforms.Edit{}}
				byPath[f.FilePath] = fc
				order = append(order, f.FilePath)
			}
			fc.LastEditAt = p.TimeCreated
			fc.Additions += f.Additions
			fc.Deletions += f.Deletions
			fc.EditCount++
			if fc.Patch != "" {
				fc.Patch += "\n"
			}
			fc.Patch += f.Patch
			fc.Edits = append(fc.Edits, platforms.Edit{PartID: p.ID, MessageID: p.MessageID, TimeCreated: p.TimeCreated, Tool: "apply_patch",
				Additions: f.Additions, Deletions: f.Deletions, Patch: f.Patch})
		}
	}
	for _, path := range order {
		fc := byPath[path]
		if fc.Additions == 0 && fc.Deletions == 0 {
			continue
		}
		out.Files = append(out.Files, *fc)
		out.TotalAdditions += fc.Additions
		out.TotalDeletions += fc.Deletions
	}
	out.FilesChanged = len(out.Files)
	return out, nil
}

// relative shows a path inside the session directory relative to it.
func relative(path, dir string) string {
	if dir == "" {
		return path
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}

// InfoFromStored fills the session info panel from stored replies: lifetime
// token totals, the message breakdown and the context the latest turn ended
// with. Codex reports no MCP or LSP servers here, so Supported stays false.
func InfoFromStored(ctx context.Context, detail *platforms.SessionDetail) (*platforms.SessionInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if detail == nil || detail.Session == nil {
		return nil, platforms.ErrNotFound
	}
	info := &platforms.SessionInfo{SessionID: detail.Session.ID, MCPServers: []platforms.MCPServer{}, LSPServers: []platforms.LSPServer{}}
	for _, m := range detail.Messages {
		var data struct {
			Role   string `json:"role"`
			Tokens *struct {
				Input     int64 `json:"input"`
				Output    int64 `json:"output"`
				Reasoning int64 `json:"reasoning"`
				Cache     struct {
					Read  int64 `json:"read"`
					Write int64 `json:"write"`
				} `json:"cache"`
			} `json:"tokens"`
			ContextTokens int64 `json:"contextTokens"`
			ContextLimit  int64 `json:"contextLimit"`
		}
		if json.Unmarshal(m.Data, &data) != nil {
			continue
		}
		switch data.Role {
		case "user":
			info.Messages.User++
		case "assistant":
			info.Messages.Assistant++
		}
		if t := data.Tokens; t != nil {
			info.Tokens.Input += t.Input
			info.Tokens.Output += t.Output + t.Reasoning
			info.Tokens.CacheRead += t.Cache.Read
			info.Tokens.CacheWrite += t.Cache.Write
		}
		if data.ContextTokens > 0 {
			info.Context.Tokens, info.Context.Limit = data.ContextTokens, data.ContextLimit
		}
	}
	return info, nil
}
