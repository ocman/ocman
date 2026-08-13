package share_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/share"
)

func TestOpenStore(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name    string
		spec    string
		wantErr string
	}{
		{name: "bare path", spec: root},
		{name: "disk scheme", spec: "disk://" + root},
		{name: "file scheme", spec: "file://" + root},
		{name: "s3 not implemented", spec: "s3://bucket/prefix", wantErr: "not implemented"},
		{name: "unknown scheme", spec: "gcs://bucket", wantErr: "unsupported"},
		{name: "empty", spec: "", wantErr: "empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := share.OpenStore(tc.spec)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("OpenStore(%q) succeeded, want error containing %q", tc.spec, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("OpenStore(%q): %v", tc.spec, err)
			}
			disk, ok := s.(*share.DiskStore)
			if !ok {
				t.Fatalf("got %T, want *share.DiskStore", s)
			}
			want, _ := filepath.Abs(root)
			if disk.Root != want {
				t.Fatalf("root = %q, want %q", disk.Root, want)
			}
		})
	}
}
