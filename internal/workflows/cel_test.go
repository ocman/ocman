package workflows

import "testing"

func TestWorkflowCEL(t *testing.T) {
	for _, test := range []struct {
		name string
		expr string
		want bool
		err  bool
	}{
		{"truth", `outcomes["check"].state == "successful"`, true, false},
		{"missing input", `artifacts["check.result"].ok == true`, false, true},
		{"invalid", `secrets.token == "x"`, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := evaluateCEL(test.expr, map[string]any{"check": map[string]any{"state": "successful"}})
			if (err != nil) != test.err || got != test.want {
				t.Fatalf("evaluateCEL() = %v, %v; want %v, error=%v", got, err, test.want, test.err)
			}
		})
	}
}

func TestValidateCELRejectsUnauthorizedAndNonBoolean(t *testing.T) {
	for _, expression := range []string{`secrets.token == "x"`, `1 + 1`} {
		if err := validateCEL(expression); err == nil {
			t.Fatalf("validateCEL(%q) accepted unauthorized expression", expression)
		}
	}
}
