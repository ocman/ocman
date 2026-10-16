package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/hostsvc/local"
	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/ocv2"
	"github.com/NoUseFreak/ocman/internal/state"
)

type serviceAuthRuntime struct {
	ocruntime.Runtime
	launches atomic.Int32
	stops    atomic.Int32
}

func (r *serviceAuthRuntime) Launch(context.Context, ocruntime.LaunchSpec) (*ocruntime.Instance, error) {
	r.launches.Add(1)
	return nil, errors.New("unexpected launch beside a live service")
}

func (r *serviceAuthRuntime) Stop(context.Context, *ocruntime.Instance) error {
	r.stops.Add(1)
	return errors.New("unexpected stop of an external service")
}

func TestStartupServiceCredentialsNeverLaunchCompetingServer(t *testing.T) {
	t.Cleanup(ocv2.SetInstalledV2(true))
	for _, tc := range []struct {
		name, env, file string
		wantAuthError   bool
	}{
		{"mismatched environment", "configured-pw", "", true},
		{"mismatched password file", "", "configured-pw", true},
		{"matching explicit password", "service-pw", "", false},
		{"service password default", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("OPENCODE_DB", "")
			t.Setenv(opencodeServerPasswordEnv, tc.env)
			config := ocv2.ServiceConfigPath()
			if err := os.MkdirAll(filepath.Dir(config), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(config, []byte(`{"password":"service-pw"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			passwordFile := ""
			if tc.file != "" {
				passwordFile = filepath.Join(t.TempDir(), "password")
				if err := os.WriteFile(passwordFile, []byte(tc.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			configured, err := resolveOpenCodePassword(passwordFile, false)
			if err != nil {
				t.Fatal(err)
			}
			useService := sharesServiceDB(pinOpenCodeDB(db.DefaultDBPath(), false))
			if !useService {
				t.Fatal("startup did not select the default database's service")
			}
			password, err := startupOpenCodePassword(t.Context(), st, configured, true, useService)
			if err != nil {
				t.Fatal(err)
			}
			var probes atomic.Int32
			service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				probes.Add(1)
				user, pw, ok := r.BasicAuth()
				if !ok || user != ocapi.DefaultUsername || pw != "service-pw" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				_, _ = w.Write([]byte(`{}`))
			}))
			defer service.Close()
			rt := &serviceAuthRuntime{Runtime: ocruntime.NewNativeRuntimeWithAuth(ocapi.New(password))}
			host := local.New(local.Deps{
				Runtime: rt,
				OpenCodeService: func() (ocv2.Service, error) {
					return ocv2.Service{URL: service.URL, PID: 4242, Version: "2.0.31"}, nil
				},
			})
			for range 2 {
				res, err := host.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{})
				if tc.wantAuthError {
					if !errors.Is(err, ocapi.ErrAuthentication) || res != nil {
						t.Fatalf("res=%+v err=%v; want authentication rejection", res, err)
					}
					if !strings.Contains(err.Error(), "check configured OpenCode credentials") || strings.Contains(err.Error(), password) {
						t.Fatalf("error should explain the mismatch without exposing credentials: %v", err)
					}
				} else if err != nil || res.Endpoint != service.URL || res.Launched {
					t.Fatalf("res=%+v err=%v; want the existing service", res, err)
				}
			}
			if rt.launches.Load() != 0 || rt.stops.Load() != 0 || probes.Load() == 0 {
				t.Fatalf("launches=%d stops=%d probes=%d; want only HTTP probes", rt.launches.Load(), rt.stops.Load(), probes.Load())
			}
		})
	}
}

func TestStartupOpenCodePasswordSelection(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		v2, enabled, service bool
		configured           string
	}{
		{"disabled", true, false, true, ""},
		{"v1", false, true, true, ""},
		{"explicit", true, true, true, "configured-pw"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(ocv2.SetInstalledV2(tc.v2))
			got, err := startupOpenCodePassword(t.Context(), nil, tc.configured, tc.enabled, tc.service)
			if err != nil || got != tc.configured {
				t.Fatalf("got=%q err=%v; want unchanged configuration", got, err)
			}
		})
	}
	t.Run("isolated database", func(t *testing.T) {
		t.Cleanup(ocv2.SetInstalledV2(true))
		t.Setenv(opencodeServerPasswordEnv, "")
		st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		got, err := startupOpenCodePassword(t.Context(), st, "", true, false)
		if err != nil || got == "" {
			t.Fatalf("got=%q err=%v; want a managed password", got, err)
		}
		stored, _, err := st.GetSecretSetting(t.Context(), v2PasswordSetting)
		if err != nil || stored != got {
			t.Fatalf("stored=%q err=%v; want the managed password", stored, err)
		}
	})
}
