package sessioncatalog

import "testing"

func TestPendingCreationDoesNotBlockOlderNativeSession(t *testing.T) {
	s, _ := openStore(t)
	reserved, _, err := s.Reserve(t.Context(), reservation())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		created  int64
		deferred int
	}{
		{"older", reserved.CreatedAt - 1, 0},
		{"same-time", reserved.CreatedAt, 1},
		{"later", reserved.CreatedAt + 1, 1},
		{"unknown", 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := s.Reconcile(t.Context(), catalogSource(), []NativeSession{{ID: tc.name, Directory: reserved.Directory, CreatedAt: tc.created}})
			if err != nil {
				t.Fatal(err)
			}
			if result.Deferred != tc.deferred {
				t.Fatalf("discovery deferred %d; want %d", result.Deferred, tc.deferred)
			}
		})
	}
}
