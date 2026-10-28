package sessionlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func (rulesTestPlatform) Capabilities() platforms.Capabilities { return platforms.Capabilities{} }

type nativeRulesPlatform struct {
	*rulesTestPlatform
	err error
}

func (*nativeRulesPlatform) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{NativeAutoPermission: true}
}
func (p *nativeRulesPlatform) NativePermissionState(context.Context, string) (*platforms.NativePermissionState, error) {
	return &platforms.NativePermissionState{Mode: "auto"}, p.err
}

func TestNativePermissionRulesRead(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		native bool
	}{
		{"managed", nil, 200, true},
		{"external", platforms.ErrNotFound, 200, false},
		{"older supervisor", platforms.ErrUnsupported, 200, false},
		{"disconnected", errors.New("offline"), 502, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &nativeRulesPlatform{rulesTestPlatform: &rulesTestPlatform{}, err: tc.err}
			m := NewModule(Deps{WithSessionAdapter: func(w http.ResponseWriter, r *http.Request, fn func(http.ResponseWriter, *http.Request, string, platforms.Platform)) {
				fn(w, r, "s1", p)
			}})
			rr := httptest.NewRecorder()
			m.HandleSessionPermissionRulesGet(rr, httptest.NewRequest(http.MethodGet, "/", nil))
			if rr.Code != tc.status {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
			if rr.Code == 200 {
				var body map[string]json.RawMessage
				if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if (body["nativeMode"] != nil) != tc.native {
					t.Fatal(rr.Body.String())
				}
			}
		})
	}
}
