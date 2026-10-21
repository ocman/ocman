package platforms

import "context"

// HelperSessionIdentity adopts an owner-local integration's newly created
// helper before an application callback observes it. Its input is private to
// the creating integration, never an application execution reference.
type HelperSessionIdentity interface {
	HelperSessionIdentity(context.Context, string) (string, error)
}
