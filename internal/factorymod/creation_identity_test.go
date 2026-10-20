package factorymod

import "testing"

func TestFactoryCreationIdentityPreservesOriginAndUsesFreshRequestKeys(t *testing.T) {
	for _, tc := range []struct{ attempt, epic, work, want string }{
		{"attempt", "epic", "work", "attempt"},
		{"", "epic", "work", "epic"},
		{"", "", "work", "work"},
	} {
		first := factoryCreationIdentity(tc.attempt, tc.epic, tc.work)
		second := factoryCreationIdentity(tc.attempt, tc.epic, tc.work)
		if first.Origin != "factory" || first.OriginID != tc.want || first.RequestKey == "" || first.RequestKey == second.RequestKey {
			t.Fatalf("invalid factory intent: %+v %+v", first, second)
		}
	}
}
