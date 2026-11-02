package hostsvc

import "github.com/ocman/ocman/internal/tasksvc"

// BackgroundTaskHost exposes the owner's application work independently of its
// coding-agent platforms. Older hosts may not implement it.
type BackgroundTaskHost interface {
	BackgroundTaskReader() tasksvc.Reader
}
