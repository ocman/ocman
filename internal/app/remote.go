package app

import (
	"context"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/server"
	"github.com/NoUseFreak/ocman/internal/state"
	log "github.com/sirupsen/logrus"
)

func validateRemoteTransport(listenAddr, tlsCert, tlsKey string, trustedOverlay bool) error {
	if listenAddr == "" {
		return nil
	}
	if (tlsCert == "") != (tlsKey == "") {
		return fmt.Errorf("remote TLS certificate and key must be provided together")
	}
	if tlsCert == "" && !trustedOverlay {
		return fmt.Errorf("remote TLS is required unless -remote-trusted-overlay is set")
	}
	return nil
}

// Remote access is opt-in; a listener failure must not block the dashboard.
func startRemoteServer(ctx context.Context, srv *server.Server, stateDB *state.DB, ident state.InstanceIdentity, listenAddr, tlsCert, tlsKey string, trustedOverlay bool) (bool, string, bool) {
	if listenAddr == "" {
		return false, "", false
	}
	rsrv := remote.NewServer(srv.Registry(), srv.RemoteServerHost(), ident.InstanceID, version).
		UseInboxStore(stateDB).UsePlugins(srv.RemotePluginOperation).UseSessions(srv.SessionService()).
		UseWebhookDispatcher(srv.RoutineService()).UseSessionEnricher(srv.EnrichRemoteSessionDetail).UseEventProxy(srv.ProxyRemoteSessionEvents)
	ln, err := remote.NewListener(remote.ListenConfig{Addr: listenAddr, Token: ident.RemoteToken, TLSCertFile: tlsCert, TLSKeyFile: tlsKey, TrustedOverlay: trustedOverlay}, rsrv)
	if err != nil {
		log.WithError(err).Error("remote: failed to start gRPC server; continuing without it")
		return false, "", false
	}
	if !ln.TLS() {
		log.WithField("addr", ln.Addr()).Warn("remote: gRPC bearer tokens are unencrypted; plaintext transport is safe only over an encrypted trusted overlay such as Tailscale or WireGuard")
	}
	go func() {
		if err := ln.Serve(); err != nil {
			log.WithError(err).Warn("remote: gRPC server stopped")
		}
	}()
	go func() { <-ctx.Done(); ln.Stop() }()
	transport := "trusted-overlay"
	if ln.TLS() {
		transport = "tls"
	}
	log.WithFields(log.Fields{"addr": ln.Addr(), "transport": transport}).Info("remote: gRPC server listening")
	return true, ln.Addr(), ln.TLS()
}
