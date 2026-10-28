// Package ocapi contains the host-local authentication shared by every
// ocman client that talks to OpenCode.
package ocapi

import (
	"fmt"
	"net/http"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/opencode/apiv2"
)

const DefaultUsername = "opencode"

// ErrAuthentication is OpenCode's authentication failure. It wraps the
// platform-neutral platforms.ErrPlatformUnauthenticated so shared handlers
// can classify it without knowing OpenCode.
var ErrAuthentication = fmt.Errorf("OpenCode authentication failed: %w", platforms.ErrPlatformUnauthenticated)

// Auth keeps the OpenCode server password opaque outside this package.
// Its formatting methods deliberately redact the value.
type Auth struct{ password string }

func New(password string) Auth { return Auth{password: password} }

func (a Auth) String() string { return "[redacted]" }

func (a Auth) GoString() string { return "ocapi.Auth{[redacted]}" }

func (a Auth) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }

// AddServerEnv injects the password only when authentication is enabled.
func (a Auth) AddServerEnv(env map[string]string) {
	if a.password != "" {
		env["OPENCODE_SERVER_USERNAME"] = DefaultUsername
		env["OPENCODE_SERVER_PASSWORD"] = a.password
		env["OPENCODE_PASSWORD"] = a.password // v2 name; v1 ignores it
	}
}

// Enabled reports whether a password is configured.
func (a Auth) Enabled() bool { return a.password != "" }

// Transport scopes Basic Auth and 401/403 classification to OpenCode
// calls, and translates v1 routes for OpenCode v2 servers (ocv2.Wrap).
func (a Auth) Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return ocv2.Wrap(a.basic(base))
}

func (a Auth) basic(base http.RoundTripper) http.RoundTripper {
	return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		clone.Header = req.Header.Clone()
		if a.password != "" {
			clone.SetBasicAuth(DefaultUsername, a.password)
		}
		resp, err := base.RoundTrip(clone)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			resp.Body.Close()
			return nil, fmt.Errorf("%w: upstream HTTP %d", ErrAuthentication, resp.StatusCode)
		}
		return resp, nil
	})
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
