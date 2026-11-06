package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const testIconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="12" cy="12" r="8"/><path stroke-linecap="round" stroke-linejoin="round" d="M4 12h16"/></svg>`

func TestIconMetadata(t *testing.T) {
	base := Description{ID: "org.example.documents", Name: "Documents", Version: "1", Protocol: Version{Major: 1}, Scope: ScopeOwner, MaxConcurrency: 1}
	for _, icon := range []*IconDescriptor{nil, {Bootstrap: "journal-text"}, {Bootstrap: "unknown-host-icon"}, {Path: "assets/wrench.svg"}} {
		d := base
		d.Icon = icon
		if err := d.Validate(); err != nil {
			t.Fatalf("%+v: %v", icon, err)
		}
		raw, _ := json.Marshal(d)
		var roundtrip Description
		if json.Unmarshal(raw, &roundtrip) != nil || !reflect.DeepEqual(d, roundtrip) {
			t.Fatalf("roundtrip: %s", raw)
		}
		n, err := Negotiate(base.Protocol, []Capability{AssetCapability}, d)
		want := 0
		if icon != nil && icon.Path != "" {
			want = 1
		}
		if err != nil || len(n.Capabilities) != want || len(d.RequestedGrants) != 0 || len(d.Capabilities) != 0 {
			t.Fatalf("implicit standard method: %+v %v", n, err)
		}
		old, err := Negotiate(base.Protocol, nil, d)
		if err != nil || len(old.Capabilities) != 0 {
			t.Fatalf("older host: %+v %v", old, err)
		}
	}
	for _, icon := range []*IconDescriptor{{}, {Bootstrap: "bi journal"}, {Bootstrap: "x", Path: "x.svg"}, {Path: "../x.svg"}, {Bootstrap: strings.Repeat("a", 129)}} {
		d := base
		d.Icon = icon
		if d.Validate() == nil {
			t.Fatalf("accepted %+v", icon)
		}
	}
	base.Capabilities = []Capability{AssetCapability}
	if base.Validate() == nil {
		t.Fatal("asset must not become opt-in capability metadata")
	}
}

func TestIconPathsAndAssetCodec(t *testing.T) {
	for _, path := range []string{"", "/a.svg", "a//b.svg", "./a.svg", "a/../b.svg", `a\b.svg`, "https://example.org/a.svg", "//example.org/a.svg", "a.svg?x", "a.svg#x", "%2e%2e/a.svg", "a.png", strings.Repeat("a", 257) + ".svg"} {
		if ValidIconPath(path) {
			t.Fatalf("accepted path %q", path)
		}
		raw, _ := json.Marshal(AssetRequest{Path: path})
		if _, err := DecodeAssetRequest(raw); err == nil {
			t.Fatalf("decoded path %q", path)
		}
	}
	for _, raw := range []string{`{"path":"a.svg","extra":true}`, `{"path":null}`, `{"path":"a.svg","path":"b.svg"}`} {
		if _, err := DecodeAssetRequest([]byte(raw)); err == nil {
			t.Fatalf("decoded %s", raw)
		}
	}
	raw, err := EncodeAssetResult([]byte(testIconSVG))
	data, decoded := DecodeAssetResult(raw)
	if err != nil || decoded != nil || string(data) != testIconSVG {
		t.Fatalf("result: %s %v %v", data, err, decoded)
	}
	for _, raw := range []string{`{}`, `{"data":"@@"}`, `{"data":null}`, `{"data":""}`, strings.Repeat(" ", MaxIconBytes*2+1)} {
		if _, err := DecodeAssetResult([]byte(raw)); err == nil {
			t.Fatalf("accepted result %q", raw[:min(len(raw), 100)])
		}
	}
}

func TestSafeIconSVG(t *testing.T) {
	for _, svg := range []string{testIconSVG, `<svg><g transform="translate(1 2) scale(2)"><rect x="1" y="2" width="10" height="8" rx="2" fill="#fff"/><ellipse cx="4" cy="3" rx="1" ry="2"/><line x1="1" x2="3" y1="2" y2="4"/><polyline points="1,2 3,4"/><polygon points="1,2 3,4 5,6"/><title>Tools</title><desc>Maintenance</desc></g></svg>`, `<svg><!-- neutral --><path d="M1 2L3 4" fill-rule="evenodd" clip-rule="nonzero" stroke-linecap="butt" stroke-linejoin="bevel" opacity="0.5"/></svg>`} {
		if err := ValidateIconSVG([]byte(svg)); err != nil {
			t.Fatalf("valid SVG: %s %v", svg, err)
		}
	}
	for _, svg := range []string{
		"", "<svg>", "<circle/>", "<svg/><svg/>", "x<svg/>", "<svg/>x", "<svg><svg/></svg>", "<svg>\xff</svg>",
		`<?xml version="1.0"?><svg/>`, `<!DOCTYPE svg [<!ENTITY x "abc">]><svg>&x;</svg>`,
		`<svg><script>alert(1)</script></svg>`, `<svg onload="alert(1)"/>`, `<svg><foreignObject/></svg>`,
		`<svg><style>path{fill:red}</style></svg>`, `<svg style="fill:red"/>`, `<svg><use href="#x"/></svg>`,
		`<svg xmlns="https://evil.example"/>`, `<svg xmlns:x="https://evil.example"><x:path/></svg>`,
		`<svg><image href="https://evil.example/x"/></svg>`, `<svg fill="url(https://evil.example/x)"/>`,
		`<svg fill="red"/>`, `<svg stroke-linecap="evil"/>`, `<svg stroke-linejoin="evil"/>`, `<svg fill-rule="evil"/>`,
		`<svg transform="url(x)"/>`, `<svg d="javascript:evil"/>`, `<svg width="calc(1px)"/>`, `<svg width=""/>`, `<svg width="1" width="2"/>`,
		"<svg>" + strings.Repeat("<g>", 33) + strings.Repeat("</g>", 33) + "</svg>",
		"<svg>" + strings.Repeat("<path/>", 2048) + "</svg>", strings.Repeat("x", MaxIconBytes+1),
	} {
		if ValidateIconSVG([]byte(svg)) == nil {
			t.Fatalf("unsafe SVG accepted: %.120s", svg)
		}
		if _, err := EncodeAssetResult([]byte(svg)); err == nil {
			t.Fatal("unsafe result encoded")
		}
	}
}
