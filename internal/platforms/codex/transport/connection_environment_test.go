package codextransport

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestConnectionEnvironment(t *testing.T) {
	base := []string{"HOME=/user", "PATH=/bin", "KEEP=value=with=equals", "CODEX_HOME=/personal",
		"OPENAI_API_KEY=test-override", "CODEX_API_KEY=another-override", "CODEX_ACCESS_TOKEN=test-access",
		"OPENAI_IDENTITY_TOKEN_FILE=/identity", "OPENAI_FEDERATION_RULE_ID=rule", "OPENAI_BASE_URL=https://other.invalid",
		"OPENAI_ORG_ID=other-org", "OPENAI_PROJECT_ID=other-project", "CODEX_HOME=/duplicate", "MCP_VARIABLE=keep"}
	before := slices.Clone(base)
	got := connectionEnvironment(base, "/accounts/work with spaces")
	want := []string{"HOME=/user", "PATH=/bin", "KEEP=value=with=equals", "MCP_VARIABLE=keep", "CODEX_HOME=/accounts/work with spaces"}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(base, before) {
		t.Fatalf("scoped environment: %q; base mutated=%v", got, !reflect.DeepEqual(base, before))
	}
	if got := connectionEnvironment(nil, "/account"); !reflect.DeepEqual(got, []string{"CODEX_HOME=/account"}) {
		t.Fatalf("nil environment could inherit the process: %q", got)
	}
}

func TestNamedSupervisorEnvironmentAndValidation(t *testing.T) {
	t.Setenv("CODEX_HOME", "/inherited")
	t.Setenv("OPENAI_API_KEY", "test-override")
	t.Setenv("CODEX_API_KEY", "test-override")
	t.Setenv("CODEX_ACCESS_TOKEN", "test-override")
	base := []string{"--socket", "/s", "--spool", "/sp", "--repo", "/r"}
	args := append(slices.Clone(base), "--codex-home", "/explicit", "--connection-account")
	a, err := parseSupervisorArgs(args)
	if err != nil || !a.ConnectionAccount {
		t.Fatalf("named supervisor arguments: %+v %v", a, err)
	}
	opts := a.options()
	for _, entry := range opts.Env {
		if strings.HasPrefix(entry, "OPENAI_API_KEY=") || strings.HasPrefix(entry, "CODEX_API_KEY=") || strings.HasPrefix(entry, "CODEX_ACCESS_TOKEN=") {
			t.Fatal("named supervisor inherited another account's authentication")
		}
	}
	if !slices.Contains(opts.Env, "CODEX_HOME=/explicit") || os.Getenv("CODEX_HOME") != "/inherited" {
		t.Fatal("named supervisor changed the process-global account")
	}
	for _, home := range []string{"", "relative"} {
		if _, err := parseSupervisorArgs(append(slices.Clone(base), "--connection-account", "--codex-home", home)); err == nil {
			t.Fatalf("named supervisor accepted implicit/relative home %q", home)
		}
	}
	// The legacy machine integration keeps its original inherited auth behavior.
	a.ConnectionAccount = false
	if !slices.Contains(a.options().Env, "OPENAI_API_KEY=test-override") {
		t.Fatal("legacy machine account authentication changed")
	}
}
