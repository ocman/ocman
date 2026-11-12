package factorymod

import "testing"

func TestFactoryModelPlatform(t *testing.T) {
	if got := factoryModelPlatform("cnx_default", "cnx_phase:p/m"); got != "cnx_phase" {
		t.Fatal(got)
	}
	if got := factoryModelPlatform("cnx_default", "p/m"); got != "cnx_default" {
		t.Fatal(got)
	}
}
