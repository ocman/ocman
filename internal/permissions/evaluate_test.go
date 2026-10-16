package permissions

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

func TestAllowsExact(t *testing.T) {
	exact := providerprotocol.ExactPattern
	rule := func(pattern, action string) platforms.PermissionRule {
		return platforms.PermissionRule{Permission: "bash", Pattern: pattern, Action: action}
	}
	tests := []struct {
		name     string
		rules    []platforms.PermissionRule
		patterns []string
		want     bool
	}{
		{"escaped star grant is exact", []platforms.PermissionRule{rule(exact("cat *"), "allow")}, []string{exact("cat /etc/shadow")}, false},
		{"escaped star grant allows itself", []platforms.PermissionRule{rule(exact("cat *"), "allow")}, []string{exact("cat *")}, true},
		{"escaped question grant is exact", []platforms.PermissionRule{rule(exact("rm foo?"), "allow")}, []string{exact("rm foo/x")}, false},
		{"glob rule globs", []platforms.PermissionRule{rule("git *", "allow")}, []string{exact("git status"), "git"}, true},
		{"empty rule pattern matches all", []platforms.PermissionRule{rule("", "allow")}, []string{exact("ls")}, true},
		{"last match wins", []platforms.PermissionRule{rule("*", "allow"), rule("rm *", "deny")}, []string{exact("rm x")}, false},
		{"no rules asks", nil, []string{"ls"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AllowsExact(tt.rules, "bash", tt.patterns); got != tt.want {
				t.Fatalf("AllowsExact = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAllows(t *testing.T) {
	yolo := []platforms.PermissionRule{{Permission: "*", Pattern: "*", Action: "allow"}}
	autoEdit := []platforms.PermissionRule{
		{Permission: "edit", Pattern: "*", Action: "allow"},
		{Permission: "bash", Pattern: "*", Action: "ask"},
	}
	tests := []struct {
		name       string
		rules      []platforms.PermissionRule
		permission string
		patterns   []string
		want       bool
	}{
		{"no rules asks", nil, "bash", []string{"ls"}, false},
		{"yolo allows bash", yolo, "bash", []string{"rm -rf build", "ls"}, true},
		{"yolo allows no patterns", yolo, "webfetch", nil, true},
		{"auto-edit allows edit", autoEdit, "edit", []string{"src/a.go"}, true},
		{"auto-edit asks bash", autoEdit, "bash", []string{"ls"}, false},
		{"last match wins", append(append([]platforms.PermissionRule{}, yolo...), platforms.PermissionRule{Permission: "bash", Pattern: "*", Action: "deny"}), "bash", []string{"ls"}, false},
		{"later yolo overrides deny", append([]platforms.PermissionRule{{Permission: "bash", Pattern: "*", Action: "deny"}}, yolo...), "bash", []string{"ls"}, true},
		{"every pattern must allow", []platforms.PermissionRule{{Permission: "bash", Pattern: "git *", Action: "allow"}}, "bash", []string{"git status", "rm x"}, false},
		{"trailing space-star matches bare command", []platforms.PermissionRule{{Permission: "bash", Pattern: "git *", Action: "allow"}}, "bash", []string{"git"}, true},
		{"question mark is one char", []platforms.PermissionRule{{Permission: "read", Pattern: "a?.txt", Action: "allow"}}, "read", []string{"ab.txt"}, true},
		{"regex chars are literal", []platforms.PermissionRule{{Permission: "read", Pattern: "a.(b)", Action: "allow"}}, "read", []string{"axxb"}, false},
		{"backslashes compare as slashes", []platforms.PermissionRule{{Permission: "read", Pattern: `C:\src\*`, Action: "allow"}}, "read", []string{"C:/src/a"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Allows(tt.rules, tt.permission, tt.patterns); got != tt.want {
				t.Fatalf("Allows = %v, want %v", got, tt.want)
			}
		})
	}
}
