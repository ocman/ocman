package codextransport

import (
	"context"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

var (
	_ platforms.Integration                = (*Platform)(nil)
	_ platforms.HistoryReader              = (*Platform)(nil)
	_ platforms.SessionIdentityProbe       = (*Platform)(nil)
	_ platforms.LifecycleReader            = (*Platform)(nil)
	_ platforms.PermissionRefresher        = (*Platform)(nil)
	_ platforms.ReplyOutcomeReporter       = (*Platform)(nil)
	_ platforms.LiveDirectoryCatalogReader = (*Platform)(nil)
	_ platforms.StoredDetailProjector      = (*Platform)(nil)
	_ interface {
		Account(context.Context) (*providerprotocol.Account, error)
	} = (*Platform)(nil)
	_ hostsvc.RuntimeEnsurer      = (*Platform)(nil)
	_ hostsvc.RuntimeAvailability = (*Platform)(nil)
)
