package protocol

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func uiDescription() Description {
	return Description{ID: "org.example.ui", Name: "UI", Version: "1", Protocol: Version{Major: 1}, MaxConcurrency: 1, Scope: ScopeOwner,
		Capabilities: []Capability{UICapability}, RequestedGrants: []string{UIAppGrant},
		UI: &UIDescriptor{SDK: "^1.0.0", Exposes: map[string]UIExpose{UIIndexExpose: NewUIExpose([]byte("export default 1"))}}}
}

func TestUIDescriptionValidation(t *testing.T) {
	if err := uiDescription().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Description){
		"missing descriptor": func(d *Description) { d.UI = nil },
		"undeclared":         func(d *Description) { d.Capabilities = nil },
		"hub scope":          func(d *Description) { d.Scope = ScopeHub },
		"missing grant":      func(d *Description) { d.RequestedGrants = nil },
		"bad range":          func(d *Description) { d.UI.SDK = "latest" },
		"no exposes":         func(d *Description) { d.UI.Exposes = nil },
		"bad expose name":    func(d *Description) { d.UI.Exposes["Bad Name"] = d.UI.Exposes[UIIndexExpose] },
		"bad checksum":       func(d *Description) { d.UI.Exposes[UIIndexExpose] = UIExpose{SHA256: "abc", Size: 1} },
		"empty bundle":       func(d *Description) { e := d.UI.Exposes[UIIndexExpose]; e.Size = 0; d.UI.Exposes[UIIndexExpose] = e },
		"oversized bundle": func(d *Description) {
			e := d.UI.Exposes[UIIndexExpose]
			e.Size = MaxUIBundleBytes + 1
			d.UI.Exposes[UIIndexExpose] = e
		},
		"too many exposes": func(d *Description) {
			for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
				d.UI.Exposes[n] = d.UI.Exposes[UIIndexExpose]
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			d := uiDescription()
			change(&d)
			if d.Validate() == nil {
				t.Fatal("accepted")
			}
		})
	}
	// Future entry points are accepted; this host only loads index.
	d := uiDescription()
	d.UI.Exposes["settings"] = d.UI.Exposes[UIIndexExpose]
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestUIDescriptionRoundTripsThroughCodec(t *testing.T) {
	d := uiDescription()
	hello := Envelope{Type: TypeHello, Hello: &Hello{Mode: ModeDescribe, Token: strings.Repeat("ab", 32), Description: &d}}
	var buf bytes.Buffer
	if err := NewEncoder(&buf).Encode(hello); err != nil {
		t.Fatal(err)
	}
	got, err := NewDecoder(&buf).Decode()
	if err != nil || got.Hello.Description.UI.Exposes[UIIndexExpose] != d.UI.Exposes[UIIndexExpose] {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestUIAllowed(t *testing.T) {
	d := uiDescription()
	if !d.UIAllowed([]string{UIAppGrant}) || d.UIAllowed(nil) {
		t.Fatal("grant not enforced")
	}
	d.UI.SDK = "^2.0.0"
	if d.UIAllowed([]string{UIAppGrant}) {
		t.Fatal("incompatible SDK allowed")
	}
	d = uiDescription()
	d.Capabilities = []Capability{{Name: "ui", Version: Version{Major: 2}}}
	if d.UIAllowed([]string{UIAppGrant}) {
		t.Fatal("other major allowed")
	}
}

func TestUISDKCompatible(t *testing.T) {
	for _, tc := range []struct {
		r, v string
		ok   bool
	}{
		{"^1.0.0", "1.0.0", true}, {"^1.0.0", "1.4.2", true}, {"^1.2.0", "1.1.9", false}, {"^1.0.0", "2.0.0", false},
		{"^0.3.1", "0.3.9", true}, {"^0.3.1", "0.4.0", false}, {"^0.0.3", "0.0.4", false}, {"^0.0.3", "0.0.3", true},
		{"~1.2.0", "1.2.7", true}, {"~1.2.0", "1.3.0", false}, {">=1.1.0", "3.0.0", true}, {">=1.1.0", "1.0.9", false},
		{"1.0.0", "1.0.0", true}, {"1.0.0", "1.0.1", false}, {"^1", "1.0.0", false}, {"^1.0.0", "v1.0.0", false},
		{"^01.0.0", "1.0.0", false}, {"", "1.0.0", false},
	} {
		if got := UISDKCompatible(tc.r, tc.v); got != tc.ok {
			t.Errorf("%q %q = %v", tc.r, tc.v, got)
		}
	}
	if ValidUISDKRange(strings.Repeat("1", 65)) || ValidUISDKRange("^99999999.0.0") {
		t.Fatal("unbounded range accepted")
	}
}

func TestUICallAndBundleCodecs(t *testing.T) {
	c, err := DecodeUICall([]byte(`{"method":"items.list","params":{"page":1}}`))
	if err != nil || c.Method != "items.list" || string(c.Params) != `{"page":1}` {
		t.Fatal(c, err)
	}
	if _, err := DecodeUICall([]byte(`{"method":"listItems"}`)); err != nil {
		t.Fatal("camel case method rejected", err)
	}
	for _, bad := range []string{`{}`, `{"method":"1x"}`, `{"method":"a b"}`, `{"method":"a","extra":1}`, `{"Method":"a"}`, `null`} {
		if _, err := DecodeUICall([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	if r, err := DecodeUIBundleRequest([]byte(`{"expose":"index"}`)); err != nil || r.Expose != "index" {
		t.Fatal(r, err)
	}
	if _, err := DecodeUIBundleRequest([]byte(`{"expose":""}`)); err == nil {
		t.Fatal("empty expose accepted")
	}
	bundle := bytes.Repeat([]byte("x"), UIBundleChunkBytes+10)
	chunks, err := EncodeUIBundleChunks(bundle)
	if err != nil || len(chunks) != 2 {
		t.Fatal(len(chunks), err)
	}
	var assembled []byte
	for _, chunk := range chunks {
		if len(chunk) > MaxMessageBytes {
			t.Fatal("chunk exceeds frame limit")
		}
		part, err := DecodeUIBundleChunk(chunk)
		if err != nil {
			t.Fatal(err)
		}
		assembled = append(assembled, part...)
	}
	if VerifyUIBundle(NewUIExpose(bundle), assembled) != nil || VerifyUIBundle(NewUIExpose(bundle), assembled[1:]) == nil {
		t.Fatal("verification")
	}
	for _, bad := range []string{`{"data":""}`, `{"data":"!!"}`, `{}`} {
		if _, err := DecodeUIBundleChunk([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	oversized, _ := json.Marshal(UIBundleChunk{Data: make([]byte, UIBundleChunkBytes+1)})
	if _, err := DecodeUIBundleChunk(oversized); err == nil {
		t.Fatal("oversized chunk accepted")
	}
}

func TestUISDKVersionMatchesPublishedPackage(t *testing.T) {
	data, err := os.ReadFile("../plugin-ui/package.json")
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &pkg) != nil || pkg.Version != UISDKVersion {
		t.Fatalf("sdk/plugin-ui is %q, host constant is %q", pkg.Version, UISDKVersion)
	}
}
