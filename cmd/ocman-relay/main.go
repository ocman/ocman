// Command ocman-relay serves ocman's cross-machine conversation share
// relay.
//
// The relay stores encrypted conversation chunks and serves the viewer
// that renders them. It never holds plaintext: chunks are sealed by the
// sharing ocman instance and the key travels only in the viewer URL's
// fragment, which browsers do not send to servers.
//
// It is deliberately a single static binary with a storage directory and
// no database. Run it behind a TLS-terminating proxy.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/NoUseFreak/ocman/internal/relay"
	"github.com/NoUseFreak/ocman/internal/share"
	"github.com/NoUseFreak/ocman/internal/webui"
)

// shutdownGrace is how long in-flight requests get to finish after a
// termination signal.
const shutdownGrace = 10 * time.Second

// Request timeouts. The relay is internet-facing, so an unbounded read
// or write would be a trivial resource exhaustion vector.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 60 * time.Second
	writeTimeout      = 120 * time.Second
	idleTimeout       = 120 * time.Second
)

func main() {
	addr := flag.String("addr", ":8231", "address to listen on")
	store := flag.String("store", "disk://./relay-data", "storage backend (disk:///path or a bare path; s3:// reserved)")
	ttl := flag.Duration("ttl", relay.DefaultTTL, "how long a share is retained")
	maxChunkBytes := flag.Int64("max-chunk-bytes", relay.DefaultMaxChunkBytes, "maximum size of one appended chunk")
	maxChunks := flag.Int("max-chunks", relay.DefaultMaxChunks, "maximum number of chunks per share")
	maxShareBytes := flag.Int64("max-share-bytes", relay.DefaultMaxShareBytes, "maximum total bytes per share")
	maxInboxBodyBytes := flag.Int64("max-inbox-body-bytes", relay.DefaultMaxInboxBodyBytes, "maximum size of one webhook inbox delivery")
	maxInboxHeaderBytes := flag.Int64("max-inbox-header-bytes", relay.DefaultMaxInboxHeaderBytes, "maximum retained webhook header bytes")
	maxInboxPendingBytes := flag.Int64("max-inbox-pending-bytes", relay.DefaultMaxInboxPendingBytes, "maximum pending ciphertext bytes per inbox")
	maxInboxPendingDeliveries := flag.Int("max-inbox-pending-deliveries", relay.DefaultMaxInboxPendingDeliveries, "maximum pending deliveries per inbox")
	inboxIngestPerHour := flag.Float64("inbox-ingest-per-hour", relay.DefaultInboxIngestPerHour, "webhook ingests allowed per inbox per hour")
	inboxIngestBurst := flag.Float64("inbox-ingest-burst", relay.DefaultInboxIngestBurst, "webhook ingest burst capacity per inbox")
	inboxTTL := flag.Duration("inbox-ttl", relay.DefaultInboxTTL, "how long an inbox is retained")
	inboxSecretHeader := flag.String("inbox-secret-header", "X-Webhook-Secret", "header name used for optional inbox shared-secret validation")
	createPerHour := flag.Float64("create-per-hour", relay.DefaultCreatePerHour, "share creations allowed per client address per hour")
	createBurst := flag.Float64("create-burst", relay.DefaultCreateBurst, "burst capacity for share creation")
	trustProxy := flag.Bool("trust-proxy", false, "read the client address from X-Forwarded-For (only behind a proxy that overwrites it)")
	noViewer := flag.Bool("no-viewer", false, "serve the API only, without the bundled viewer")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	backend, err := share.OpenStore(*store)
	if err != nil {
		log.Error("opening store", "error", err)
		os.Exit(1)
	}

	cfg := relay.Config{
		Store:                     backend,
		MaxChunkBytes:             *maxChunkBytes,
		MaxChunks:                 *maxChunks,
		MaxShareBytes:             *maxShareBytes,
		MaxInboxBodyBytes:         *maxInboxBodyBytes,
		MaxInboxHeaderBytes:       *maxInboxHeaderBytes,
		MaxInboxPendingBytes:      *maxInboxPendingBytes,
		MaxInboxPendingDeliveries: *maxInboxPendingDeliveries,
		InboxIngestPerHour:        *inboxIngestPerHour,
		InboxIngestBurst:          *inboxIngestBurst,
		InboxTTL:                  *inboxTTL,
		InboxSecretHeader:         *inboxSecretHeader,
		TTL:                       *ttl,
		CreatePerHour:             *createPerHour,
		CreateBurst:               *createBurst,
		TrustProxy:                *trustProxy,
		EnrollmentToken:           os.Getenv("OCMAN_RELAY_INBOX_ENROLLMENT_TOKEN"),
	}
	if !*noViewer {
		assets, err := webui.FS()
		if err != nil {
			log.Error("loading bundled viewer", "error", err)
			os.Exit(1)
		}
		cfg.Assets = assets
	}

	srv, err := relay.New(cfg)
	if err != nil {
		log.Error("building relay", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go srv.Run(ctx, func(err error) { log.Error("expiry sweep", "error", err) })

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	go func() {
		log.Info("relay listening", "addr", *addr, "store", *store, "ttl", ttl.String(), "viewer", !*noViewer)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("serving", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown", "error", err)
		os.Exit(1)
	}
}
