package permissions

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type fakeLister struct {
	rows []state.ApprovedPermission
	err  error
}

func (f fakeLister) ListApprovedPermissions(context.Context, string, string) ([]state.ApprovedPermission, error) {
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
			rules, count, err := BuildInheritedRules(t.Context(), fakeLister{rows: tc.rows}, "opencode", "ses-parent")
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

	rules, count, err := BuildInheritedRules(t.Context(), fakeLister{rows: rows}, "opencode", "ses-parent")
	if err != nil {
		t.Fatalf("BuildInheritedRules: %v", err)
	}
	if count != maxInheritedPatterns || len(rules) != maxInheritedPatterns {
		t.Fatalf("count/len = %d/%d, want %d", count, len(rules), maxInheritedPatterns)
	}
}

func TestBuildInheritedRules_NilListerAndEmptyParent(t *testing.T) {
	if rules, count, err := BuildInheritedRules(t.Context(), nil, "opencode", "ses"); err != nil || rules != nil || count != 0 {
		t.Errorf("nil lister: got (%v,%d,%v), want (nil,0,nil)", rules, count, err)
	}
	if rules, count, err := BuildInheritedRules(t.Context(), fakeLister{}, "opencode", ""); err != nil || rules != nil || count != 0 {
		t.Errorf("empty parent: got (%v,%d,%v), want (nil,0,nil)", rules, count, err)
	}
}

func TestBuildInheritedRules_ListerError(t *testing.T) {
	_, _, err := BuildInheritedRules(t.Context(), fakeLister{err: errors.New("boom")}, "opencode", "ses")
	if err == nil {
		t.Fatal("expected error from lister to propagate")
	}
}

type fakeLiveReader struct {
	rules []platforms.PermissionRule
	err   error
}

func (f fakeLiveReader) PermissionRules(string, string) ([]platforms.PermissionRule, error) {
	return f.rules, f.err
}

// A YOLO parent has a live ruleset (edit/bash/webfetch = allow) but no
// recorded "Allow always" clicks. Approval-only inheritance yields
// nothing; merging the live ruleset must propagate the YOLO posture.
func TestBuildInheritedRulesWithLive_YoloParentWithNoApprovals(t *testing.T) {
	live := []platforms.PermissionRule{
		{Permission: "edit", Pattern: "*", Action: "allow"},
		{Permission: "bash", Pattern: "*", Action: "allow"},
		{Permission: "webfetch", Pattern: "*", Action: "allow"},
	}
	rules, count, err := BuildInheritedRulesWithLive(t.Context(),
		fakeLister{}, // no approvals
		fakeLiveReader{rules: live},
		"opencode", "ses-parent",
	)
	if err != nil {
		t.Fatalf("BuildInheritedRulesWithLive: %v", err)
	}
	if count != 3 || !equalRules(rules, live) {
		t.Fatalf("got (%d, %+v), want (3, %+v)", count, rules, live)
	}
}

// Live rules follow the approval-derived rules so a live rule wins on
// conflict (OpenCode evaluates the last matching rule).
func TestBuildInheritedRulesWithLive_MergeOrder(t *testing.T) {
	rules, _, err := BuildInheritedRulesWithLive(t.Context(),
		fakeLister{rows: []state.ApprovedPermission{ap("bash", "git *")}},
		fakeLiveReader{rules: []platforms.PermissionRule{
			{Permission: "bash", Pattern: "*", Action: "allow"},
		}},
		"opencode", "ses-parent",
	)
	if err != nil {
		t.Fatalf("BuildInheritedRulesWithLive: %v", err)
	}
	want := []platforms.PermissionRule{
		{Permission: "bash", Pattern: "git *", Action: "allow"}, // from approvals, first
		{Permission: "bash", Pattern: "*", Action: "allow"},     // from live, last (wins)
	}
	if !equalRules(rules, want) {
		t.Fatalf("rules = %+v, want %+v", rules, want)
	}
}

// A live-reader error is soft: fall back to approval-only rules.
func TestBuildInheritedRulesWithLive_LiveErrorFallsBack(t *testing.T) {
	rules, count, err := BuildInheritedRulesWithLive(t.Context(),
		fakeLister{rows: []state.ApprovedPermission{ap("edit", "*.go")}},
		fakeLiveReader{err: errors.New("boom")},
		"opencode", "ses-parent",
	)
	if err != nil {
		t.Fatalf("BuildInheritedRulesWithLive: %v", err)
	}
	want := []platforms.PermissionRule{{Permission: "edit", Pattern: "*.go", Action: "allow"}}
	if count != 1 || !equalRules(rules, want) {
		t.Fatalf("got (%d, %+v), want (1, %+v)", count, rules, want)
	}
}

// A nil live reader degrades to plain approval-only inheritance.
func TestBuildInheritedRulesWithLive_NilReader(t *testing.T) {
	rules, count, err := BuildInheritedRulesWithLive(t.Context(),
		fakeLister{rows: []state.ApprovedPermission{ap("edit", "*.go")}},
		nil,
		"opencode", "ses-parent",
	)
	if err != nil {
		t.Fatalf("BuildInheritedRulesWithLive: %v", err)
	}
	if count != 1 || !equalRules(rules, []platforms.PermissionRule{{Permission: "edit", Pattern: "*.go", Action: "allow"}}) {
		t.Fatalf("got (%d, %+v)", count, rules)
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
