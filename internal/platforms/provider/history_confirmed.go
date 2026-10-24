package provider

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

func (a *Adapter) ConfirmHistory(ctx context.Context, id string, receipt providerprotocol.HistoryConfirmation) error {
	return a.call(ctx, a.hello.Capabilities.HistoryConfirm, "POST", sessionPath(id, "/history-confirmed"), receipt, nil)
}

func (a *Adapter) SupportsHistoryConfirmation() bool { return a.hello.Capabilities.HistoryConfirm }
