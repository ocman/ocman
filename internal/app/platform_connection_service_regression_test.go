package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/ocruntime"
	"github.com/ocman/ocman/internal/platformconnections"
	ocapi "github.com/ocman/ocman/internal/platforms/opencode/api"
	ocv2 "github.com/ocman/ocman/internal/platforms/opencode/apiv2"
	"github.com/ocman/ocman/internal/state/statetest"
)

type registeredConnectionRuntime struct {
	endpoint                string
	probeErr                error
	launches, stops, probes int
}

func (r *registeredConnectionRuntime) Launch(context.Context, ocruntime.LaunchSpec) (*ocruntime.Instance, error) {
	r.launches++
	return nil, errors.New("competing writer launch forbidden")
}
func (r *registeredConnectionRuntime) Probe(_ context.Context, inst *ocruntime.Instance) error {
	r.probes++
	if inst.Kind != ocruntime.KindService || inst.Endpoint != r.endpoint {
		return errors.New("probed outside registered service")
	}
	return r.probeErr
}
func (r *registeredConnectionRuntime) Stop(context.Context, *ocruntime.Instance) error {
	r.stops++
	return errors.New("registered service is not owned")
}

func TestMigratedFactoryWithCustomRuntimeRootNeverDuplicatesRegisteredService(t *testing.T) {
	for _, mode := range []string{"healthy", "busy", "authentication-rejected"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			core := statetest.Open(t, platformconnections.Schema)
			store := platformconnections.New(core)
			if _, err := store.Initialize(t.Context(), platformconnections.Seed{ExistingInstallation: true, Connections: []platformconnections.Input{{Kind: "opencode", Name: "Original", Configuration: "existing", Directory: t.TempDir(), Enabled: true}}}); err != nil {
				t.Fatal(err)
			}
			c, err := store.Legacy(t.Context(), "opencode")
			if err != nil {
				t.Fatal(err)
			}
			nativePath := db.DefaultDBPath()
			if err := os.MkdirAll(filepath.Dir(nativePath), 0700); err != nil {
				t.Fatal(err)
			}
			native, err := db.OpenReadWrite(nativePath)
			if err != nil {
				t.Fatal(err)
			}
			if err := native.Close(); err != nil {
				t.Fatal(err)
			}
			runtime := &registeredConnectionRuntime{endpoint: "http://127.0.0.1:48391"}
			switch mode {
			case "busy":
				runtime.probeErr = context.DeadlineExceeded
			case "authentication-rejected":
				runtime.probeErr = ocapi.ErrAuthentication
			}
			record, err := json.Marshal(map[string]any{"url": runtime.endpoint, "pid": os.Getpid(), "version": "2.0.31"})
			if err != nil {
				t.Fatal(err)
			}
			for path, body := range map[string][]byte{ocv2.ServiceStatePath(): record, ocv2.ServiceConfigPath(): []byte(`{"password":"service-test-password"}`)} {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, body, 0600); err != nil {
					t.Fatal(err)
				}
			}
			factory := connectionFactory{state: core, dataDir: t.TempDir(), openCodeDB: nativePath, legacyAuth: ocapi.New("service-test-password"), runtimeBuilder: func(ocruntime.NativeConnectionConfig, ocapi.Auth) (ocruntime.Runtime, error) { return runtime, nil }}
			for range 2 {
				p, err := factory.openCode(t.Context(), c)
				if mode == "healthy" {
					if err != nil {
						t.Fatalf("registered service not adopted: %v", err)
					}
					p.(*openCodeConnection).Close()
				} else if !errors.Is(err, runtime.probeErr) {
					t.Fatalf("service policy bypassed: %v", err)
				}
			}
			if runtime.probes != 2 || runtime.launches != 0 || runtime.stops != 0 {
				t.Fatalf("registered service ownership broken: probes=%d launches=%d stops=%d", runtime.probes, runtime.launches, runtime.stops)
			}
			scoped, err := platformconnections.NewRuntimeStore(t.Context(), core, c)
			if err != nil {
				t.Fatal(err)
			}
			if roots, err := scoped.ListManagedProviders(t.Context(), "opencode"); err != nil || len(roots) != 0 {
				t.Fatalf("external service persisted as owned: %v %v", roots, err)
			}
		})
	}
}
