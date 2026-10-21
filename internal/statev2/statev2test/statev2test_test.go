package statev2test

import "testing"

func TestOpenReturnsUsableStore(t *testing.T) {
	store := Open(t)
	if instances, err := store.Instances(t.Context(), "owner", "test"); err != nil || len(instances) != 0 {
		t.Fatalf("fresh store: %+v %v", instances, err)
	}
}
