package inbox

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func TestPermissionIdentityWarningIdentifiersAndReasons(t *testing.T) {
	logger := log.StandardLogger()
	hooks := logger.ReplaceHooks(make(log.LevelHooks))
	t.Cleanup(func() { logger.ReplaceHooks(hooks) })
	hook := logtest.NewGlobal()
	for _, tc := range []struct {
		err    error
		reason string
	}{
		{errors.New("read failed"), "identity_lookup_failed"},
		{errPermissionAdapterUnavailable, "adapter_not_found"},
		{context.DeadlineExceeded, "identity_lookup_interrupted"},
		{context.Canceled, "identity_lookup_interrupted"},
		{sessioncatalog.ErrInvalid, "invalid_identity"},
		{platforms.ErrNotFound, "identity_not_found"},
		{sessioncatalog.ErrNotFound, "identity_not_found"},
		{platforms.ErrPlatformUnreachable, "owner_unavailable"},
		{platforms.ErrHistoryUnavailable, "identity_source_unavailable"},
		{sessioncatalog.ErrCreating, "identity_not_ready"},
	} {
		permissionIdentityWarning(tc.err, "resolve", "cnx_account", "canonical-session", "request")
		entry := hook.LastEntry()
		loggedError, _ := entry.Data[log.ErrorKey].(error)
		if entry.Level != log.WarnLevel || entry.Data["operation"] != "resolve" || entry.Data["platform"] != "cnx_account" || entry.Data["sessionID"] != "canonical-session" || entry.Data["permissionID"] != "request" || entry.Data["reason"] != tc.reason || !errors.Is(loggedError, tc.err) {
			t.Fatalf("missing diagnostic context: %+v", entry)
		}
	}
}

func TestMissingPermissionResolverDoesNotMintIdentity(t *testing.T) {
	if key, _, err := NewModule(Deps{}).permissionKey(t.Context(), "opencode", "native", "request"); key != "" || !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("missing resolver minted identity: %q, %v", key, err)
	}
}
