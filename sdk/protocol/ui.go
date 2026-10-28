package protocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// UICapability lets an approved plugin ship one in-app React bundle per
// declared expose and answer plugin-defined calls from that bundle.
var UICapability = Capability{Name: "ui", Version: Version{Major: 1}}

const (
	// UIAppGrant approves running the plugin's UI code inside ocman with the
	// user's session, and its ui.v1 calls.
	UIAppGrant = "ui.app"
	// UISDKVersion is the host's @ocman/plugin-ui release. Plugins declare a
	// compatible range; sdk/plugin-ui/package.json must carry the same value.
	UISDKVersion = "1.0.0"
	// UIIndexExpose is the routed application entry point mounted at
	// /plugins/<id>/*. Other expose names are reserved for later entry points.
	UIIndexExpose = "index"
	// MaxUIBundleBytes keeps the base64 chunks of one bundle under the 8 MiB
	// per-call output limit.
	MaxUIBundleBytes = 4 << 20
	// UIBundleChunkBytes is the raw size of one streamed bundle chunk.
	UIBundleChunkBytes = 512 << 10
	maxUIExposes       = 8
)

// UIDescriptor binds each expose's bundle checksum into the approved
// description, so a changed bundle requires approval again.
type UIDescriptor struct {
	SDK     string              `json:"sdk"`
	Exposes map[string]UIExpose `json:"exposes"`
}

type UIExpose struct {
	SHA256 string `json:"sha256"`
	Size   int    `json:"size"`
}

// UICall is the params of a ui.v1 "call": a plugin-defined method and JSON.
type UICall struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// UIBundleRequest is the params of a ui.v1 "bundle" call. The plugin streams
// UIBundleChunk chunks, then returns {}.
type UIBundleRequest struct {
	Expose string `json:"expose"`
}

type UIBundleChunk struct {
	Data []byte `json:"data"`
}

var (
	uiMethod = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,127}$`)
	sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)
	semverRe = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
)

// NewUIExpose describes bundle bytes for a UIDescriptor.
func NewUIExpose(bundle []byte) UIExpose {
	sum := sha256.Sum256(bundle)
	return UIExpose{SHA256: hex.EncodeToString(sum[:]), Size: len(bundle)}
}

func (d Description) hasUI() bool {
	return slices.ContainsFunc(d.Capabilities, func(c Capability) bool { return c.Name == UICapability.Name })
}

func (d Description) validateUI() error {
	if !d.hasUI() {
		if d.UI != nil {
			return ErrInvalidMessage
		}
		return nil
	}
	ui := d.UI
	if ui == nil || d.Scope != ScopeOwner || !slices.Contains(d.RequestedGrants, UIAppGrant) ||
		!ValidUISDKRange(ui.SDK) || len(ui.Exposes) == 0 || len(ui.Exposes) > maxUIExposes {
		return ErrInvalidMessage
	}
	for name, expose := range ui.Exposes {
		if !validName(name) || !sha256Re.MatchString(expose.SHA256) || expose.Size < 1 || expose.Size > MaxUIBundleBytes {
			return ErrInvalidMessage
		}
	}
	return nil
}

// UIAllowed reports whether the browser may load this plugin's UI and call it.
// Grants are the current approvals; incompatible SDK ranges are refused.
func (d Description) UIAllowed(grants []string) bool {
	return d.Scope == ScopeOwner && d.UI != nil && slices.ContainsFunc(d.Capabilities, func(c Capability) bool {
		return c.Name == UICapability.Name && c.Version.Major == UICapability.Version.Major
	}) && slices.Contains(grants, UIAppGrant) && UISDKCompatible(d.UI.SDK, UISDKVersion)
}

// DecodeUICall validates a ui.v1 call's params.
func DecodeUICall(data []byte) (UICall, error) {
	var c UICall
	if decodePaneJSON(data, &c) != nil || !uiMethod.MatchString(c.Method) || len(c.Params) > 0 && !json.Valid(c.Params) {
		return UICall{}, ErrInvalidMessage
	}
	return c, nil
}

// DecodeUIBundleRequest validates a ui.v1 bundle request.
func DecodeUIBundleRequest(data []byte) (UIBundleRequest, error) {
	var r UIBundleRequest
	if decodePaneJSON(data, &r) != nil || !validName(r.Expose) {
		return UIBundleRequest{}, ErrInvalidMessage
	}
	return r, nil
}

// DecodeUIBundleChunk validates one streamed chunk of bundle bytes.
func DecodeUIBundleChunk(data []byte) ([]byte, error) {
	var c struct {
		Data string `json:"data"`
	}
	if decodePaneJSON(data, &c) != nil || len(c.Data) > base64.StdEncoding.EncodedLen(UIBundleChunkBytes) {
		return nil, ErrInvalidMessage
	}
	bundle, err := base64.StdEncoding.DecodeString(c.Data)
	if err != nil || len(bundle) == 0 || len(bundle) > UIBundleChunkBytes {
		return nil, ErrInvalidMessage
	}
	return bundle, nil
}

// VerifyUIBundle checks assembled bytes against their approved declaration.
func VerifyUIBundle(expose UIExpose, bundle []byte) error {
	if NewUIExpose(bundle) != expose {
		return ErrInvalidMessage
	}
	return nil
}

// ValidUISDKRange accepts "1.2.3", "^1.2.3", "~1.2.3" and ">=1.2.3".
func ValidUISDKRange(r string) bool {
	_, _, ok := parseUIRange(r)
	return ok
}

// UISDKCompatible reports whether version satisfies the semver range r.
// Caret ranges allow changes that keep the left-most nonzero component.
func UISDKCompatible(r, version string) bool {
	op, low, ok := parseUIRange(r)
	v, valid := parseSemver(version)
	if !ok || !valid || slices.Compare(v[:], low[:]) < 0 {
		return false
	}
	switch op {
	case "":
		return v == low
	case ">=":
		return true
	case "~":
		return v[0] == low[0] && v[1] == low[1]
	default: // ^
		switch {
		case low[0] > 0:
			return v[0] == low[0]
		case low[1] > 0:
			return v[0] == 0 && v[1] == low[1]
		default:
			return v == low
		}
	}
}

func parseUIRange(r string) (string, [3]int, bool) {
	if len(r) > 64 {
		return "", [3]int{}, false
	}
	op := ""
	for _, prefix := range []string{">=", "^", "~"} {
		if rest, found := strings.CutPrefix(r, prefix); found {
			op, r = prefix, rest
			break
		}
	}
	v, ok := parseSemver(r)
	return op, v, ok
}

func parseSemver(s string) ([3]int, bool) {
	m := semverRe.FindStringSubmatch(s)
	var v [3]int
	if m == nil {
		return v, false
	}
	for i := range v {
		n, err := strconv.Atoi(m[i+1])
		if err != nil || n > 1<<20 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// EncodeUIBundleChunks splits bundle bytes into the plugin's chunk payloads.
func EncodeUIBundleChunks(bundle []byte) ([]json.RawMessage, error) {
	var chunks []json.RawMessage
	for part := range slices.Chunk(bundle, UIBundleChunkBytes) {
		data, err := json.Marshal(UIBundleChunk{Data: bytes.Clone(part)})
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, data)
	}
	return chunks, nil
}
