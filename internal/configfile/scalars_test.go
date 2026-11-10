package configfile

import "testing"

func TestDocumentCanonicalizesTypedScalars(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"false", "false"}, {"False", "false"}, {"FALSE", "false"},
		{"True", "true"}, {"TRUE", "true"}, {"0x10", "16"},
		{"1_000", "1000"}, {`"False"`, "False"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			doc, err := Parse([]byte("version: 1\nsettings:\n  value: " + tc.input + "\n"))
			if err != nil {
				t.Fatal(err)
			}
			value, ok, err := doc.Get("settings", "value")
			if err != nil || !ok || value != tc.want {
				t.Fatalf("value = %q, %v; want %q", value, err, tc.want)
			}
		})
	}
}
