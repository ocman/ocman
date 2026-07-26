package workflows

import (
	"fmt"

	"github.com/google/cel-go/cel"
)

const celCostLimit = 1_000

// workflowCELEnv deliberately exposes only completed Node Results. It has no
// host, secret, filesystem, artifact, or template bindings.
func workflowCELEnv() (*cel.Env, error) {
	return cel.NewEnv(
		cel.Variable("outcomes", cel.MapType(cel.StringType, cel.DynType)),
	)
}

func validateCEL(expression string) error {
	if expression == "" {
		return fmt.Errorf("CEL expression is required")
	}
	env, err := workflowCELEnv()
	if err != nil {
		return err
	}
	ast, issues := env.Compile(expression)
	if issues.Err() != nil {
		return fmt.Errorf("invalid CEL: %w", issues.Err())
	}
	if ast.OutputType() != cel.BoolType {
		return fmt.Errorf("CEL expression must return bool")
	}
	return nil
}

func evaluateCEL(expression string, outcomes map[string]any) (bool, error) {
	env, err := workflowCELEnv()
	if err != nil {
		return false, err
	}
	ast, issues := env.Compile(expression)
	if issues.Err() != nil {
		return false, fmt.Errorf("invalid CEL: %w", issues.Err())
	}
	program, err := env.Program(ast, cel.CostLimit(celCostLimit))
	if err != nil {
		return false, err
	}
	value, _, err := program.Eval(map[string]any{"outcomes": outcomes})
	if err != nil {
		return false, err
	}
	result, ok := value.Value().(bool)
	if !ok {
		return false, fmt.Errorf("CEL expression must return bool")
	}
	return result, nil
}

// EvaluateCondition evaluates an edge condition against completed Node
// Results. The external-runner shim needs the same evaluator the native
// dispatcher uses, so the sandboxed environment stays the single
// definition of what a condition may see.
func EvaluateCondition(expression string, outcomes map[string]any) (bool, error) {
	return evaluateCEL(expression, outcomes)
}
