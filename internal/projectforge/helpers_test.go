package projectforge

import (
	"context"
	"testing"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/hostsvc"
)

func newTestModule() *Module {
	return NewModule(Deps{})
}

func githubUpstreams(root string) *hostsvc.ProjectUpstreams {
	return &hostsvc.ProjectUpstreams{RepoRoot: root, Remotes: []forge.Remote{{Name: "origin", Type: forge.RemoteTypeGitHub, Host: "github.com", Repo: "alice/myproj"}}}
}

// fakeHost answers upstream detection; everything else panics.
type fakeHost struct {
	hostsvc.Host
	upstreams     *hostsvc.ProjectUpstreams
	upstreamErr   error
	upstreamCalls int
	onUpstream    func()
	remoteID      string
}

func (h *fakeHost) RemoteID() string {
	if h.remoteID != "" {
		return h.remoteID
	}
	return "rem1"
}

func (h *fakeHost) ProjectUpstreams(context.Context, string) (*hostsvc.ProjectUpstreams, error) {
	h.upstreamCalls++
	if h.onUpstream != nil {
		h.onUpstream()
	}
	return h.upstreams, h.upstreamErr
}

func TestEffectivePerPageAndMissingForgeResolver(t *testing.T) {
	if effectivePerPage(forge.ListOptions{}) != 30 || effectivePerPage(forge.ListOptions{PerPage: 5}) != 5 {
		t.Fatal("per-page default")
	}
	if f, ok := newTestModule().resolveForge(forge.Remote{}); f != nil || ok {
		t.Fatal("resolved a forge without a resolver")
	}
}
