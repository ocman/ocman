package protocol

import (
	"encoding/base64"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
)

// IconDescriptor is optional display metadata, not a permission request.
// Exactly one of Bootstrap (a bare icon name) and Path (a plugin SVG) is set.
type IconDescriptor struct {
	Bootstrap string `json:"bootstrap,omitempty"`
	Path      string `json:"path,omitempty"`
}

// AssetCapability is implicitly offered by descriptions with an SVG icon.
// Plugins must not add it to Capabilities or request an asset grant.
var AssetCapability = Capability{Name: "asset", Version: Version{Major: 1}}

const MaxIconBytes = 64 << 10

type AssetRequest struct {
	Path string `json:"path"`
}

type AssetResult struct {
	Data []byte `json:"data"`
}

var iconName = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var assetSegment = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func ValidIconPath(path string) bool {
	if path == "" || len(path) > 256 || !strings.HasSuffix(path, ".svg") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "." || part == ".." || !assetSegment.MatchString(part) {
			return false
		}
	}
	return true
}

func (d Description) validateIcon() error {
	if slices.ContainsFunc(d.Capabilities, func(c Capability) bool { return c.Name == AssetCapability.Name }) {
		return ErrInvalidMessage
	}
	i := d.Icon
	if i == nil {
		return nil
	}
	if (i.Bootstrap == "") == (i.Path == "") || i.Bootstrap != "" && (len(i.Bootstrap) > 128 || !iconName.MatchString(i.Bootstrap)) || i.Path != "" && !ValidIconPath(i.Path) {
		return ErrInvalidMessage
	}
	return nil
}

func (d Description) effectiveCapabilities() []Capability {
	caps := slices.Clone(d.Capabilities)
	if d.Icon != nil && d.Icon.Path != "" {
		caps = append(caps, AssetCapability)
	}
	return caps
}

func DecodeAssetRequest(raw []byte) (AssetRequest, error) {
	var req AssetRequest
	if decodePaneJSON(raw, &req) != nil || !ValidIconPath(req.Path) {
		return AssetRequest{}, ErrInvalidMessage
	}
	return req, nil
}

func DecodeAssetResult(raw []byte) ([]byte, error) {
	var result struct {
		Data string `json:"data"`
	}
	if len(raw) > MaxIconBytes*2 || decodePaneJSON(raw, &result) != nil {
		return nil, ErrInvalidMessage
	}
	data, err := base64.StdEncoding.DecodeString(result.Data)
	if err != nil || ValidateIconSVG(data) != nil {
		return nil, ErrInvalidMessage
	}
	return data, nil
}

func EncodeAssetResult(data []byte) (json.RawMessage, error) {
	if err := ValidateIconSVG(data); err != nil {
		return nil, err
	}
	return json.Marshal(AssetResult{Data: data})
}
