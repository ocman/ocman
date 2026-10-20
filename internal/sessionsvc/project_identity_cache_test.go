package sessionsvc

import "testing"

func TestNativeDirectoryCacheDoesNotCrossOwnersWithCollidingSessionIDs(t *testing.T) {
	svc, local := newProjectModelService(map[string][]string{"/repo": {"prov/local"}, "/remote": {"prov/remote"}})
	remote := &dirPlatform{fakePlatform: &fakePlatform{id: "r-owner:opencode", available: true}, dir: "/remote"}
	if _, _, ok := svc.projectModels(t.Context(), local, "same"); !ok {
		t.Fatal("local lookup failed")
	}
	models, _, ok := svc.projectModels(t.Context(), remote, "same")
	if !ok || len(models) != 1 || models[0] != "prov/remote" || remote.lookups != 1 {
		t.Fatalf("remote session reused local native-ID cache: %v %v, lookups %d", models, ok, remote.lookups)
	}
}
