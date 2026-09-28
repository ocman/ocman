package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/NoUseFreak/ocman/internal/state"
)

// ArtifactFileInput is one file to publish: an absolute Path (copied into
// the blob store) or an inline Name + Content.
type ArtifactFileInput struct {
	Path    string `json:"path,omitempty"`
	Name    string `json:"name,omitempty"`
	Content string `json:"content,omitempty"`
	MIME    string `json:"mime,omitempty"`
}

// ArtifactLinkInput is one http(s) link to publish.
type ArtifactLinkInput struct {
	URL   string `json:"url"`
	Label string `json:"label,omitempty"`
}

// ArtifactInput is the create request shared by every artifact producer.
type ArtifactInput struct {
	Title       string              `json:"title"`
	Description string              `json:"description,omitempty"`
	Directory   string              `json:"directory"`
	Platform    string              `json:"platform,omitempty"`
	SessionID   string              `json:"sessionId,omitempty"`
	Files       []ArtifactFileInput `json:"files,omitempty"`
	Links       []ArtifactLinkInput `json:"links,omitempty"`
}

func artifactInvalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", state.ErrArtifactInvalid, fmt.Sprintf(format, args...))
}

// artifactProject folds dir to its canonical local project root and refuses
// anything that is not a project ocman already knows about.
func (s *Server) artifactProject(ctx context.Context, dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", artifactInvalid("directory must be absolute")
	}
	root, err := factoryProjectResolver{s}.ResolveLocalProject(ctx, filepath.Clean(dir))
	if err != nil || !filepath.IsAbs(root) {
		return "", artifactInvalid("directory %q is not a known project", dir)
	}
	root = filepath.Clean(root)
	projects, err := s.router().Local().Projects(ctx)
	if err != nil {
		return "", err
	}
	worktrees := filepath.Join(filepath.Dir(root), ".worktrees", filepath.Base(root))
	for _, p := range projects {
		d := filepath.Clean(p.Directory)
		for _, base := range []string{root, worktrees} {
			if d == base || strings.HasPrefix(d, base+string(filepath.Separator)) {
				return root, nil
			}
		}
	}
	return "", artifactInvalid("directory %q is not a known project", dir)
}

// artifactMIME picks a type: explicit, then extension, then content sniff.
func artifactMIME(explicit, name string, head []byte) string {
	if explicit != "" {
		return explicit
	}
	ext := strings.ToLower(filepath.Ext(name))
	// Go's builtin table lacks markdown and the host's mime.types varies;
	// previews key off this type, so pin it.
	if ext == ".md" || ext == ".markdown" {
		return "text/markdown; charset=utf-8"
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	return http.DetectContentType(head)
}

func validArtifactName(name string) bool {
	return strings.TrimSpace(name) != "" && !strings.ContainsAny(name, `/\`) && name != "." && name != ".."
}

// storeArtifactFile copies one file input into the blob store.
func (s *Server) storeArtifactFile(f ArtifactFileInput) (state.ArtifactItem, error) {
	var r io.Reader
	name := f.Name
	if f.Path != "" {
		if f.Content != "" || !filepath.IsAbs(f.Path) {
			return state.ArtifactItem{}, artifactInvalid("file path must be absolute and exclusive with content")
		}
		file, err := os.Open(f.Path)
		if err != nil {
			return state.ArtifactItem{}, artifactInvalid("file %q cannot be read", f.Path)
		}
		defer file.Close()
		if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() {
			return state.ArtifactItem{}, artifactInvalid("file %q is not a regular file", f.Path)
		}
		if name == "" {
			name = filepath.Base(f.Path)
		}
		r = file
	} else {
		r = strings.NewReader(f.Content)
	}
	if !validArtifactName(name) {
		return state.ArtifactItem{}, artifactInvalid("file name %q is invalid", name)
	}
	head := make([]byte, 512)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return state.ArtifactItem{}, err
	}
	head = head[:n]
	sum, size, err := s.stateDB.PutArtifactBlob(io.MultiReader(bytes.NewReader(head), r))
	if errors.Is(err, state.ErrArtifactTooLarge) {
		return state.ArtifactItem{}, fmt.Errorf("%w: %w", state.ErrArtifactInvalid, err)
	}
	if err != nil {
		return state.ArtifactItem{}, err
	}
	return state.ArtifactItem{Kind: state.ArtifactItemFile, Name: name, MIME: artifactMIME(f.MIME, name, head), Size: size, SHA256: sum}, nil
}

// CreateArtifact validates in, copies its files, stores the artifact and
// announces it to every connected client.
//
// ponytail: blobs copied before a later validation failure stay on disk
// unreferenced; add a startup sweep if that ever matters.
func (s *Server) CreateArtifact(ctx context.Context, in ArtifactInput) (state.Artifact, error) {
	if s.stateDB == nil {
		return state.Artifact{}, errors.New("state database unavailable")
	}
	if strings.TrimSpace(in.Title) == "" {
		return state.Artifact{}, artifactInvalid("title is required")
	}
	if (in.Platform == "") != (in.SessionID == "") {
		return state.Artifact{}, artifactInvalid("platform and sessionId must be set together")
	}
	if len(in.Files)+len(in.Links) == 0 {
		return state.Artifact{}, artifactInvalid("at least one file or link is required")
	}
	for _, l := range in.Links {
		u, err := url.Parse(l.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return state.Artifact{}, artifactInvalid("link %q must be an http(s) URL", l.URL)
		}
	}
	for _, f := range in.Files {
		if f.Path == "" && !validArtifactName(f.Name) {
			return state.Artifact{}, artifactInvalid("file needs a path or a name")
		}
	}
	dir, err := s.artifactProject(ctx, in.Directory)
	if err != nil {
		return state.Artifact{}, err
	}
	items := make([]state.ArtifactItem, 0, len(in.Files)+len(in.Links))
	for _, f := range in.Files {
		item, err := s.storeArtifactFile(f)
		if err != nil {
			return state.Artifact{}, err
		}
		items = append(items, item)
	}
	for _, l := range in.Links {
		items = append(items, state.ArtifactItem{Kind: state.ArtifactItemLink, URL: l.URL, Label: l.Label})
	}
	a, err := s.stateDB.CreateArtifact(ctx, state.Artifact{
		Title: strings.TrimSpace(in.Title), Description: in.Description, Directory: dir,
		Platform: in.Platform, SessionID: in.SessionID, Items: items,
	})
	if err != nil {
		return state.Artifact{}, err
	}
	if payload, err := json.Marshal(map[string]string{"id": a.ID, "directory": a.Directory, "platform": a.Platform, "sessionId": a.SessionID}); err == nil {
		s.broadcastGlobalEvent("ocman.artifact.created", payload)
	}
	return a, nil
}

// ArtifactFilePath is the in-app route serving item ordinal of artifact id.
func ArtifactFilePath(id string, ordinal int) string {
	return fmt.Sprintf("/api/artifacts/%s/files/%d", url.PathEscape(id), ordinal)
}

// artifactView fills each file item's url with its serving route.
func artifactView(a state.Artifact) state.Artifact {
	a.Items = append([]state.ArtifactItem(nil), a.Items...)
	for i := range a.Items {
		if a.Items[i].Kind == state.ArtifactItemFile {
			a.Items[i].URL = ArtifactFilePath(a.ID, i)
		}
	}
	return a
}
