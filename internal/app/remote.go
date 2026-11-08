package app

import (
	"context"
	"fmt"
	"sync"

	"github.com/ocman/ocman/internal/remote"
	"github.com/ocman/ocman/internal/server"
	"github.com/ocman/ocman/internal/state"
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

// validateRemoteManaged rejects -remote-managed where no hub could ever
// reach the instance to send its settings: without a listener, or in GUI
// mode, which does not start one.
func validateRemoteManaged(listenAddr string, managed, gui bool) error {
	switch {
	case !managed:
		return nil
	case gui:
		return fmt.Errorf("-remote-managed is not supported with -gui, which does not start the remote listener")
	case listenAddr == "":
		return fmt.Errorf("-remote-managed requires -remote-listen")
	}
	return nil
}

// enableRemoteManaged switches state.db to the hub's settings snapshot.
func enableRemoteManaged(ctx context.Context, stateDB *state.DB, managed bool) error {
	if !managed {
		return nil
	}
	if err := stateDB.EnableManagedSettings(ctx); err != nil {
		return err
	}
	if snapshot, ok := stateDB.ManagedSettings(); ok {
		log.WithField("hub", snapshot.Hub).Info("remote: managed mode, using the hub's saved settings")
	} else {
		log.Info("remote: managed mode, using local settings until a hub connects")
	}
	return nil
}

// useHubManagedSettings makes the manager send this instance's settings to
// the remotes it manages, on connect and after every managed setting change.
func useHubManagedSettings(mgr *remote.Manager, srv *server.Server, stateDB *state.DB, instanceID string) {
	mgr.UseManagedSettings(hubManagedSettings(srv, stateDB, instanceID))
	stateDB.OnManagedSettingsChange(mgr.PushManagedSettings)
}

// hubManagedSettings reads this hub's effective settings at send time.
func hubManagedSettings(srv *server.Server, stateDB *state.DB, instanceID string) func(context.Context) (state.ManagedSettings, error) {
	return func(ctx context.Context) (state.ManagedSettings, error) {
		return stateDB.HubManagedSettings(ctx, instanceID, srv.AutoApproveDefault())
	}
}

// Remote access is opt-in; a listener failure must not block the dashboard.
func startRemoteServer(ctx context.Context, srv *server.Server, stateDB *state.DB, ident state.InstanceIdentity, listenAddr, tlsCert, tlsKey string, trustedOverlay bool) (bool, string, bool, func()) {
	if listenAddr == "" {
		return false, "", false, func() {}
	}
	rsrv := remote.NewServer(srv.Registry(), srv.RemoteServerHost(), ident.InstanceID, version).
		UseInboxStore(stateDB).UsePlugins(srv.RemotePluginOperation).UseSessions(srv.SessionService()).
		UsePullRequests(srv.RemotePullRequestOperation).UseBackgroundTasks(srv.BackgroundTaskReader()).
		UseSessionEnricher(srv.EnrichRemoteSessionDetail).UseEventProxy(srv.ProxyRemoteSessionEvents).
		UsePermissionNotificationDeferrer(srv.PermissionNotificationDeferred).UsePermissionPolicy(srv.SessionPermissionPolicy).
		UseAutoApprove(srv.SessionAutoApprove).UseManagedSettings(stateDB).
		UsePublishedResources(srv.ServePublishedResource).UseSessionChanges(srv.StreamRemoteSessionChanges)
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
