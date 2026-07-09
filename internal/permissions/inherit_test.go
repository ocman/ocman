package permissions

import (
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type fakeLister struct {
	rows []state.ApprovedPermission
	err  error
}

func (f fakeLister) ListApprovedPermissions(string, string) ([]state.ApprovedPermission, error) {
	return f.rows, f.err
}

func ap(text string, patterns ...string) state.ApprovedPermission {
	return state.ApprovedPermission{PermissionText: text, Patterns: patterns}
}

func TestBuildInheritedRules(t *testing.T) {
	tests := []struct {
		name  string
		rows  []state.ApprovedPermission
		want  []platforms.PermissionRule
		count int
	}{
		{
			name:  "empty parent",
			rows:  nil,
			want:  nil,
			count: 0,
		},
		{
			name: "patternable grouping and dedup",
			rows: []state.ApprovedPermission{
				ap("bash", "git *", "ls"),
				ap("bash", "git *"), // duplicate pattern -> deduped
			},
			want: []platforms.PermissionRule{
				{Permission: "bash", Pattern: "git *", Action: "allow"},
				{Permission: "bash", Pattern: "ls", Action: "allow"},
			},
			count: 2,
		},
		{
			name: "flat action key emits empty pattern once",
			rows: []state.ApprovedPermission{
				ap("todowrite"),
				ap("todowrite"), // key repeated -> still one flat rule
			},
			want: []platforms.PermissionRule{
				{Permission: "todowrite", Pattern: "", Action: "allow"},
			},
			count: 1,
		},
		{
			name: "unknown key dropped",
			rows: []state.ApprovedPermission{
				ap("bogus_tool", "x"),
				ap("edit", "*.go"),
			},
			want: []platforms.PermissionRule{
				{Permission: "edit", Pattern: "*.go", Action: "allow"},
			},
			count: 1,
		},
		{
			name: "patternable key with no patterns falls back to star",
			rows: []state.ApprovedPermission{
				ap("read"),
			},
			want: []platforms.PermissionRule{
				{Permission: "read", Pattern: "*", Action: "allow"},
			},
			count: 1,
		},
		{
			name: "richer permission text resolves to leading key",
			rows: []state.ApprovedPermission{
				ap("bash git status", "git *"),
			},
			want: []platforms.PermissionRule{
				{Permission: "bash", Pattern: "git *", Action: "allow"},
			},
			count: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rules, count, err := BuildInheritedRules(fakeLister{rows: tc.rows}, "opencode", "ses-parent")
			if err != nil {
				t.Fatalf("BuildInheritedRules: %v", err)
			}
			if count != tc.count {
				t.Errorf("count = %d, want %d", count, tc.count)
			}
			if !equalRules(rules, tc.want) {
				t.Errorf("rules = %+v, want %+v", rules, tc.want)
			}
		})
	}
}

func TestBuildInheritedRules_Truncation(t *testing.T) {
	var rows []state.ApprovedPermission
	pats := make([]string, 0, maxInheritedPatterns+50)
	for i := 0; i < maxInheritedPatterns+50; i++ {
		pats = append(pats, "p"+itoa(i))
	}
	rows = append(rows, ap("bash", pats...))

	rules, count, err := BuildInheritedRules(fakeLister{rows: rows}, "opencode", "ses-parent")
	if err != nil {
		t.Fatalf("BuildInheritedRules: %v", err)
	}
	if count != maxInheritedPatterns || len(rules) != maxInheritedPatterns {
		t.Fatalf("count/len = %d/%d, want %d", count, len(rules), maxInheritedPatterns)
	}
}

func TestBuildInheritedRules_NilListerAndEmptyParent(t *testing.T) {
	if rules, count, err := BuildInheritedRules(nil, "opencode", "ses"); err != nil || rules != nil || count != 0 {
		t.Errorf("nil lister: got (%v,%d,%v), want (nil,0,nil)", rules, count, err)
	}
	if rules, count, err := BuildInheritedRules(fakeLister{}, "opencode", ""); err != nil || rules != nil || count != 0 {
		t.Errorf("empty parent: got (%v,%d,%v), want (nil,0,nil)", rules, count, err)
	}
}

func TestBuildInheritedRules_ListerError(t *testing.T) {
	_, _, err := BuildInheritedRules(fakeLister{err: errors.New("boom")}, "opencode", "ses")
	if err == nil {
		t.Fatal("expected error from lister to propagate")
	}
}

func equalRules(a, b []platforms.PermissionRule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// itoa avoids importing strconv for the truncation test's throwaway keys.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
