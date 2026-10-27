package db

// ShouldResurfaceSession distinguishes foreground idleness from completion of
// finite background work. Services remain visible but do not delay completion.
func ShouldResurfaceSession(session Session, archivedAt int64, mode string) bool {
	if session.TimeUpdated <= archivedAt {
		return false
	}
	if mode == "activity" || session.PendingPermission || session.PendingQuestion {
		return true
	}
	return session.Status != StatusBusy && session.BackgroundTaskCount <= session.BackgroundServiceCount
}

type BackgroundTask struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Kind        string `json:"kind,omitempty"`
	Lifecycle   string `json:"lifecycle,omitempty"` // task (default) or explicitly declared service
}
