package platforms

import (
	"context"

	"github.com/ocman/ocman/internal/db"
)

type BackgroundSnapshot struct {
	Count int                 `json:"count"`
	Tasks []db.BackgroundTask `json:"tasks"`
}

type SetBackgroundLifecycleRequest struct {
	SessionID string `json:"sessionID"`
	TaskID    string `json:"taskID"`
	Lifecycle string `json:"lifecycle"`
}

type BackgroundTaskManager interface {
	BackgroundTasks(context.Context, string) (*BackgroundSnapshot, error)
	SetBackgroundLifecycle(context.Context, SetBackgroundLifecycleRequest) error
}
