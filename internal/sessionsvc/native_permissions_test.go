package sessionsvc

import (
	"github.com/NoUseFreak/ocman/internal/platforms"
	"testing"
)

func TestValidateNativePermissionUpdate(t *testing.T) {
	for _, tc := range []struct {
		mode    string
		rules   []platforms.PermissionRule
		invalid bool
	}{
		{"default", nil, false}, {"auto", nil, false}, {"yolo", nil, true}, {"", nil, true}, {"auto", []platforms.PermissionRule{}, true},
	} {
		err := validateNativePermissionUpdate(platforms.SetPermissionRulesRequest{NativeMode: &tc.mode, Rules: tc.rules})
		if (err != nil) != tc.invalid {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
	if err := validateNativePermissionUpdate(platforms.SetPermissionRulesRequest{}); err != nil {
		t.Fatal(err)
	}
}
