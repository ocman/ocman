package plugins

import "testing"

func TestConversationProjectMustBeScalar(t *testing.T) {
	d := Description{Scope: ScopeOwner, Capabilities: []Capability{ConversationCapability}, RequestedGrants: []string{ConversationSessionGrant},
		Settings: []Setting{{Key: "project", Type: "string", Required: true, List: true}}}
	if err := d.validateConversation(); err == nil {
		t.Fatal("list-valued conversation project accepted")
	}
	d.Settings[0].List = false
	if err := d.validateConversation(); err != nil {
		t.Fatal(err)
	}
}
