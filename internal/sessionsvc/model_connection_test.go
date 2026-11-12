package sessionsvc

import (
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"testing"
)

func TestModelForAdapterKeepsConnectionIdentity(t *testing.T) {
	reg := platforms.NewRegistry()
	p := &platformtest.Fake{PlatformID: "opencode"}
	other := &platformtest.Fake{PlatformID: "opencode"}
	if err := reg.RegisterConnection("cnx_work", p); err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterConnection("cnx_other", other); err != nil {
		t.Fatal(err)
	}
	svc := New(reg, Hooks{})
	if model, err := svc.modelForAdapter(p, "cnx_work:p/m"); err != nil || model != "p/m" {
		t.Fatalf("model = %s %v", model, err)
	}
	if _, err := svc.modelForAdapter(other, "cnx_work:p/m"); err == nil {
		t.Fatal("accepted sibling reference")
	}
}
