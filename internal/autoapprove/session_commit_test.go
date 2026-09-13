package autoapprove

import (
	"reflect"
	"testing"
)

func TestParseGitCommitSummaries(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		command string
		want    []commitSummary
	}{
		{"ordinary", "[feature/x abc1234] add capture\n 1 file changed\n", "git commit -m 'add capture'", []commitSummary{{SHA: "abc1234", Branch: ptr("feature/x"), Subject: "add capture"}}},
		{"root and ansi", "\x1b[32m[main (root-commit) deadbee] initial commit\x1b[0m\n", "git commit", []commitSummary{{SHA: "deadbee", Branch: ptr("main"), Subject: "initial commit"}}},
		{"detached", "[detached HEAD cafe123] detached work\n", "git commit", []commitSummary{{SHA: "cafe123", Branch: nil, Subject: "detached work"}}},
		{"multiple", "[main 111aaaa] one\n[main 222bbbb] two\n", "git commit; git commit", []commitSummary{{SHA: "111aaaa", Branch: ptr("main"), Subject: "one"}, {SHA: "222bbbb", Branch: ptr("main"), Subject: "two"}}},
		{"reject command and prose", "$ git commit -m nope\ngit commit -m nope\nThe commit failed.\n", "git commit -m nope", nil},
		{"reject echoed summary", "[main abc1234] fake\n", `echo '[main abc1234] fake'`, nil},
		{"reject formatted echo", "[main abc1234] fake\n", `printf '[main %s] fake\n' abc1234`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseGitCommitSummaries(tt.output, tt.command); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func ptr(value string) *string { return &value }
