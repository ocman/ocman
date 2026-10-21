package platforms

import "context"

// ModelOptionsReader discovers live configuration for helper execution.
// Historical usage and model statistics are read exclusively from statev2.
type ModelOptionsReader interface {
	ModelOptions(context.Context) []string
}
