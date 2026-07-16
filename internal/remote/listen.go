package remote

import (
	"crypto/tls"
	"fmt"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
)

// ListenConfig configures the remote-side gRPC server.
type ListenConfig struct {
	// Addr is the bind address (e.g. "0.0.0.0:8230").
	Addr string
	// Token is the remote-access token inbound hubs must present.
	Token string
	// TLSCertFile and TLSKeyFile enable TLS. They must be set together.
	TLSCertFile string
	TLSKeyFile  string
	// TrustedOverlay explicitly permits plaintext for an authenticated
	// private network such as Tailscale or WireGuard.
	TrustedOverlay bool
}

// Listener bundles a running gRPC server with its net.Listener so the
// caller can serve and later stop it.
type Listener struct {
	grpc *grpc.Server
	ln   net.Listener
	addr string
	tls  bool
}

// Addr returns the actual bound address (useful when Addr used :0).
func (l *Listener) Addr() string { return l.addr }

// TLS reports whether the server is using TLS.
func (l *Listener) TLS() bool { return l.tls }

// Serve blocks serving gRPC until Stop is called or a fatal error occurs.
func (l *Listener) Serve() error { return l.grpc.Serve(l.ln) }

// Stop gracefully stops the server.
func (l *Listener) Stop() { l.grpc.GracefulStop() }

// NewListener binds the address and registers the Ocman service with the
// bearer-token interceptors and optional TLS. It does not start serving;
// call Serve (typically in a goroutine).
func NewListener(cfg ListenConfig, srv *Server) (*Listener, error) {
	hasCert := cfg.TLSCertFile != ""
	hasKey := cfg.TLSKeyFile != ""
	if hasCert != hasKey {
		return nil, fmt.Errorf("remote: TLS certificate and key must be provided together")
	}
	if !hasCert && !cfg.TrustedOverlay {
		return nil, fmt.Errorf("remote: TLS is required unless trusted overlay is explicitly enabled")
	}

	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("remote: listen %s: %w", cfg.Addr, err)
	}

	unary, stream := NewAuthInterceptors(cfg.Token)
	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(unary),
		grpc.StreamInterceptor(stream),
	}

	useTLS := hasCert
	if useTLS {
		cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			ln.Close()
			return nil, fmt.Errorf("remote: loading TLS keypair: %w", err)
		}
		opts = append(opts, grpc.Creds(credentials.NewServerTLSFromCert(&cert)))
	}

	gs := grpc.NewServer(opts...)
	pb.RegisterOcmanServer(gs, srv)

	return &Listener{grpc: gs, ln: ln, addr: ln.Addr().String(), tls: useTLS}, nil
}
