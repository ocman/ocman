package agenttools

import (
	"testing"
)

func TestExamplesDeclareValidDescriptions(t *testing.T) {
	for _, p := range []Plugin{Notes("org.example.notes"), Inventory("org.example.inventory")} {
		if err := p.Description.Validate(); err != nil {
			t.Fatalf("%s: %v", p.Description.ID, err)
		}
	}
}
