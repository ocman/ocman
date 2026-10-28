package protocol

import (
	"encoding/json"
	"testing"
)

func TestPluginLocalPageRequestWithoutProject(t *testing.T) {
	if _, err := DecodePageRequest([]byte(`{"pageId":"targets"}`)); err != nil {
		t.Fatalf("plugin-local homepage must not require a project: %v", err)
	}
}

func TestPluginLocalPageAuthorizationAndVersion(t *testing.T) {
	d := pageDescription()
	d.Capabilities = []Capability{PluginPageCapability}
	d.RequestedGrants = []string{PagePluginGrant}
	d.Pages[0].Scope = "plugin"
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	request := PageRequest{PageID: "workspace"}
	if !d.PageAllowed("workspace", []string{PagePluginGrant}) || d.PageAllowed("workspace", []string{PageProjectGrant}) || !d.PageRequestAllowed(request) {
		t.Fatal("wrong plugin-local grant or scope")
	}
	if ValidatePageCall("read", request, PageContributionCapability.Version) == nil {
		t.Fatal("projectless request admitted on 1.1")
	}
	if err := ValidatePageCall("read", request, PluginPageCapability.Version); err != nil {
		t.Fatal(err)
	}
	d.Pages[0].Scope = "project"
	d.RequestedGrants = []string{PageProjectGrant}
	if d.PageRequestAllowed(request) {
		t.Fatal("project page allowed a missing identity")
	}
	d.Pages[0].Scope = "invalid"
	if d.Validate() == nil {
		t.Fatal("invalid scope accepted")
	}
	d.Pages[0].Scope = "plugin"
	d.RequestedGrants = []string{PagePluginGrant}
	d.Capabilities = []Capability{PageContributionCapability}
	if d.Validate() == nil {
		t.Fatal("plugin scope accepted on 1.1")
	}
}

func TestPluginConfigurationLinksAreTypedAndVersioned(t *testing.T) {
	doc := contributionDocument()
	doc.Collection.Rows[0].ProjectID = "project-a"
	raw, _ := json.Marshal(doc)
	if _, err := DecodePageDocument(raw); err != nil {
		t.Fatal(err)
	}
	if ValidatePageVersion(doc, PageContributionCapability.Version) == nil {
		t.Fatal("project link accepted on 1.1")
	}
	if err := ValidatePageVersion(doc, PluginPageCapability.Version); err != nil {
		t.Fatal(err)
	}
	doc.Collection.Rows[0].ProjectID = "../escape"
	raw, _ = json.Marshal(doc)
	if _, err := DecodePageDocument(raw); err == nil {
		t.Fatal("unsafe project link accepted")
	}
}
