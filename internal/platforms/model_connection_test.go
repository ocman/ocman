package platforms

import "testing"

func TestModelForConnection(t *testing.T) {
	for _, tc := range []struct {
		scope, kind, ref string
		valid            bool
	}{
		{"cnx_work", "opencode", "cnx_work:p/m", true},
		{"r-box:cnx_work", "opencode", "cnx_work:p/m", true},
		{"cnx_work", "opencode", "opencode:p/m", true},
		{"cnx_work", "opencode", "p/m", true},
		{"cnx_work", "opencode", "cnx_other:p/m", false},
		{"cnx_work", "opencode", "claudecode:p/m", false},
	} {
		t.Run(tc.scope+"/"+tc.ref, func(t *testing.T) {
			got, err := ModelForConnection(ID(tc.scope), ID(tc.kind), tc.ref)
			if tc.valid && (err != nil || got != "p/m") {
				t.Fatalf("model = %q, %v", got, err)
			}
			if !tc.valid && err == nil {
				t.Fatal("accepted another account's model")
			}
		})
	}
}
