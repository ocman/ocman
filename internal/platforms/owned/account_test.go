package owned

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

type accountNative struct {
	*nativePlatform
	account *providerprotocol.Account
	err     error
}

func (p *accountNative) Account(context.Context) (*providerprotocol.Account, error) {
	return p.account, p.err
}

type accountReader interface {
	Account(context.Context) (*providerprotocol.Account, error)
}

// The provider accounts settings list reads Account from the registered
// canonical adapter: it must reach the integration's own account.
func TestAccountReachesIntegration(t *testing.T) {
	a, p, _ := fixture(t)
	want := &providerprotocol.Account{SubscriptionType: "pro"}
	a.native = &accountNative{nativePlatform: p, account: want}
	reader, ok := any(a).(accountReader)
	if !ok {
		t.Fatal("canonical adapter does not expose the integration's account")
	}
	if got, err := reader.Account(context.Background()); err != nil || got != want {
		t.Fatalf("account = %+v %v", got, err)
	}
	a.native = &accountNative{nativePlatform: p, err: platforms.ErrNotFound}
	if _, err := reader.Account(context.Background()); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("signed out = %v", err)
	}
	// Integrations without an account are left out of the list.
	a.native = p
	if _, err := reader.Account(context.Background()); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("no account = %v", err)
	}
}
