package projectsmod

import (
	"reflect"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestProjectPermissionPresets(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want []platforms.PermissionRule
	}{
		{"", nil},
		{"default", nil},
		{"plan", []platforms.PermissionRule{{Permission: "edit", Pattern: "*", Action: "deny"}, {Permission: "bash", Pattern: "*", Action: "deny"}}},
		{"auto-edit", []platforms.PermissionRule{{Permission: "edit", Pattern: "*", Action: "allow"}, {Permission: "bash", Pattern: "*", Action: "ask"}}},
		{"yolo", []platforms.PermissionRule{{Permission: "*", Pattern: "*", Action: "allow"}}},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			d := Defaults{PermissionMode: tc.mode}
			got, err := d.PermissionRules()
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("rules = %#v, err = %v", got, err)
			}
			if err := d.validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
