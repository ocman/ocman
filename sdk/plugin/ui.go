package plugin

import (
	"context"
	"encoding/json"

	"github.com/ocman/ocman/sdk/protocol"
)

type (
	UIDescriptor    = protocol.UIDescriptor
	UIExpose        = protocol.UIExpose
	UICall          = protocol.UICall
	UIBundleRequest = protocol.UIBundleRequest
)

var UICapability = protocol.UICapability

const (
	UIAppGrant       = protocol.UIAppGrant
	UISDKVersion     = protocol.UISDKVersion
	UIIndexExpose    = protocol.UIIndexExpose
	MaxUIBundleBytes = protocol.MaxUIBundleBytes
)

// UIBundle is one built ESM bundle served to the browser for an expose.
type UIBundle struct {
	Expose string
	Data   []byte
}

// UICallFunc answers one plugin-defined method from the plugin's UI. Its
// result is marshalled to JSON. Return Failure(ErrorNotFound) for unknown
// methods and another Failure category for safe user-facing errors.
type UICallFunc func(ctx context.Context, method string, params json.RawMessage) (any, error)

// NewUIDescriptor declares bundles for Description.UI, binding each checksum
// into the approved description. sdk is the @ocman/plugin-ui semver range
// the bundles were built against, such as "^1.0.0".
func NewUIDescriptor(sdk string, bundles ...UIBundle) *UIDescriptor {
	ui := &UIDescriptor{SDK: sdk, Exposes: map[string]UIExpose{}}
	for _, b := range bundles {
		ui.Exposes[b.Expose] = protocol.NewUIExpose(b.Data)
	}
	return ui
}

// UIHandler serves ui.v1: "call" dispatches to call, and "bundle" streams the
// declared bundle bytes, which must match description.UI. Pass the same
// bundles given to NewUIDescriptor.
func UIHandler(description Description, call UICallFunc, bundles ...UIBundle) Handler {
	byExpose := map[string][]byte{}
	for _, b := range bundles {
		byExpose[b.Expose] = b.Data
	}
	return func(ctx context.Context, c Call, emit func(json.RawMessage) error) (json.RawMessage, error) {
		if c.Capability != UICapability.Name || c.Version.Major != UICapability.Version.Major || description.UI == nil {
			return nil, Failure(ErrorNotFound)
		}
		switch c.Method {
		case "call":
			request, err := protocol.DecodeUICall(c.Params)
			if err != nil {
				return nil, Failure(ErrorInvalidArgument)
			}
			if call == nil {
				return nil, Failure(ErrorNotFound)
			}
			value, err := call(ctx, request.Method, request.Params)
			if err != nil {
				return nil, err
			}
			return json.Marshal(value)
		case "bundle":
			request, err := protocol.DecodeUIBundleRequest(c.Params)
			if err != nil {
				return nil, Failure(ErrorInvalidArgument)
			}
			declared, ok := description.UI.Exposes[request.Expose]
			data, present := byExpose[request.Expose]
			if !ok || !present {
				return nil, Failure(ErrorNotFound)
			}
			if protocol.VerifyUIBundle(declared, data) != nil {
				return nil, Failure(ErrorInternal)
			}
			chunks, err := protocol.EncodeUIBundleChunks(data)
			if err != nil {
				return nil, err
			}
			for _, chunk := range chunks {
				if err := emit(chunk); err != nil {
					return nil, err
				}
			}
			return json.RawMessage(`{}`), nil
		}
		return nil, Failure(ErrorNotFound)
	}
}
