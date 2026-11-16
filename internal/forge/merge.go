package forge

import "context"

// PRMerger merges the expected head using the forge's configured default method.
// Implementations reject missing defaults rather than choose another method.
type PRMerger interface {
	MergePR(ctx context.Context, repo string, number int, headSHA string) error
}
