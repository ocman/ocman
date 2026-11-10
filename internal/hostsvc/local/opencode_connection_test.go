package local

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/ocruntime"
	"github.com/ocman/ocman/internal/platforms/opencode/apiv2"
)

func TestOpenCodeConnectionsSameProjectHaveIndependentLifecycle(t *testing.T) {
	root := t.TempDir()
	project := initRepo(t)
	// Deliberately share the fake inventory to prove the host's root filter
	// prevents reaping a sibling. Production additionally scopes persistence
	// by owner, connection ID and native kind.
	store := newFakeStore()
	aRT := &fakeRuntime{endpoint: "http://127.0.0.1:10001"}
	bRT := &fakeRuntime{endpoint: "http://127.0.0.1:10002"}
	serviceCalls := 0
	newHost := func(name string, rt *fakeRuntime) *Host {
		h, err := NewOpenCodeConnection(Deps{Runtime: rt, ManagedStore: store, OpenCodeService: func() (ocv2.Service, error) {
			serviceCalls++
			return ocv2.Service{}, errors.New("must not inspect shared service")
		}}, filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(h.Finalize)
		return h
	}
	a, b := newHost("alpha", aRT), newHost("beta", bRT)
	ensure := func(h *Host) *hostsvc.EnsureRuntimeResult {
		res, err := h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: project})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	ar, br := ensure(a), ensure(b)
	if ar.Endpoint == br.Endpoint || aRT.spec().RepoRoot == bRT.spec().RepoRoot {
		t.Fatal("same-project accounts shared a server")
	}
	physicalRoot, err := projectOpencodeRoot(t.Context(), project)
	if err != nil {
		t.Fatal(err)
	}
	if ar.RepoRoot != physicalRoot || br.RepoRoot != physicalRoot {
		t.Fatal("account root leaked into physical project identity")
	}
	if serviceCalls != 0 {
		t.Fatal("named account inspected shared background service")
	}
	a.reapNonMachineInstances(t.Context())
	if !store.has(b.machineRoot()) || aRT.stopCount() != 0 {
		t.Fatal("account cleanup touched sibling")
	}
	// Rebuild a host from durable inventory and re-probe instead of launching.
	recovered := newHost("alpha", aRT)
	if ensure(recovered).Endpoint != ar.Endpoint || aRT.launchCount() != 1 {
		t.Fatal("failed to recover private server")
	}
	if err := recovered.StopProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: recovered.machineRoot()}); err != nil {
		t.Fatal(err)
	}
	if bRT.stopCount() != 0 || !store.has(b.machineRoot()) {
		t.Fatal("stop touched sibling runtime")
	}
	if _, err := recovered.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: project}); err != nil {
		t.Fatal(err)
	}
	if aRT.launchCount() != 2 || bRT.launchCount() != 1 {
		t.Fatal("recovery switched accounts")
	}
}

func TestOpenCodeConnectionStoreRefusesOtherRootsAndKinds(t *testing.T) {
	root := t.TempDir()
	store := &openCodeConnectionStore{store: newFakeStore(), root: root}
	if err := store.Upsert(t.Context(), root, ManagedInstance{Kind: ocruntime.KindService}); err == nil {
		t.Fatal("persisted shared service")
	}
	if err := store.Upsert(t.Context(), root+"-sibling", ManagedInstance{Kind: ocruntime.KindNativeTmux}); err == nil {
		t.Fatal("persisted sibling")
	}
	if err := store.Delete(t.Context(), root+"-sibling"); err == nil {
		t.Fatal("deleted sibling")
	}
	if _, found, err := store.Get(t.Context(), root+"-sibling"); err != nil || found {
		t.Fatal("read sibling")
	}
	if err := store.store.Upsert(t.Context(), root, ManagedInstance{Kind: ocruntime.KindService}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Get(t.Context(), root); err == nil {
		t.Fatal("recovered wrong runtime kind")
	}
	if err := store.Delete(t.Context(), root); err == nil {
		t.Fatal("deleted wrong runtime kind")
	}
	if _, err := NewOpenCodeConnection(Deps{}, root); err == nil {
		t.Fatal("defaulted runtime or store")
	}
}
