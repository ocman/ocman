package app

import (
	"context"
	"fmt"
	"sync"

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
func startRemoteServer(ctx context.Context, srv *server.Server, stateDB *state.DB, ident state.InstanceIdentity, listenAddr, tlsCert, tlsKey string, trustedOverlay bool) (bool, string, bool, func()) {
	if listenAddr == "" {
		return false, "", false, func() {}
	}
	rsrv := remote.NewServer(srv.Registry(), srv.RemoteServerHost(), ident.InstanceID, version).
		UseInboxStore(stateDB).UsePlugins(srv.RemotePluginOperation).UseSessions(srv.SessionService()).
		UseSessionEnricher(srv.EnrichRemoteSessionDetail).UseEventProxy(srv.ProxyRemoteSessionEvents).
		UsePermissionNotificationDeferrer(srv.PermissionNotificationDeferred).UsePermissionPolicy(srv.SessionPermissionPolicy)
	ln, err := remote.NewListener(remote.ListenConfig{Context: ctx, Ready: srv.SessionsReady, Addr: listenAddr, Token: ident.RemoteToken, TLSCertFile: tlsCert, TLSKeyFile: tlsKey, TrustedOverlay: trustedOverlay}, rsrv)
	if err != nil {
		log.WithError(err).Error("remote: failed to start gRPC server; continuing without it")
		return false, "", false, func() {}
	}
	if !ln.TLS() {
		log.WithField("addr", ln.Addr()).Warn("remote: gRPC bearer tokens are unencrypted; plaintext transport is safe only over an encrypted trusted overlay such as Tailscale or WireGuard")
	}
	served := make(chan struct{})
	go func() {
		defer close(served)
		if err := ln.Serve(); err != nil {
			log.WithError(err).Warn("remote: gRPC server stopped")
		}
	}()
	var once sync.Once
	stop := func() { once.Do(func() { ln.Stop(); <-served }) }
	unhook := context.AfterFunc(ctx, stop)
	transport := "trusted-overlay"
	if ln.TLS() {
		transport = "tls"
	}
	log.WithFields(log.Fields{"addr": ln.Addr(), "transport": transport}).Info("remote: gRPC server listening")
	return true, ln.Addr(), ln.TLS(), func() { unhook(); stop() }
}

// Reconnect inventory can write catalogue/reference state. Do not start it while
// the owner-local cutover still requires stopped session-state consumers.
func startPreparedRemoteManager(ctx context.Context, srv *server.Server, mgr *remote.Manager) func() {
	ctx, cancel := context.WithCancel(ctx)
	started := make(chan struct{})
	go func() {
		defer close(started)
		if srv.AwaitSessionPreparation(ctx) {
			mgr.Start(ctx)
		}
	}()
	return func() { cancel(); <-started; mgr.Stop() }
}
