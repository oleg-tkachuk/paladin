package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/app"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/eventingest"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
)

// serveIngestCmd runs the storage-event consumer plane. Subscribes to
// the configured driver (webhook receiver, NATS subscriber, or
// RabbitMQ consumer) and dispatches events through dedup → handler.
//
// One driver per process — operators pick the transport in
// cfg.Ingest.Driver. Multi-driver fan-in is achievable by running
// multiple ingest pods, each with its own driver, against the same
// dedup table.
//
// Source-format adapters live in internal/eventingest/source_*.go.
// The webhook driver picks them per-route; nats/rabbitmq drivers
// pick the single configured SourceFormat.
var serveIngestCmd = &cobra.Command{
	Use:   "ingest",
	Short: "Run the storage-event consumer (webhook / NATS / RabbitMQ)",
	Run: func(cmd *cobra.Command, args []string) {
		ctx, stop := signalCtx()
		defer stop()

		cfg, l, db := boot(ctx)
		defer func() { _ = db.Close }()

		if !cfg.Ingest.Enabled {
			l.Fatal("cfg.Ingest.Enabled=false; refuse to start serve ingest with the subsystem disabled")
		}

		deps, err := app.BuildSharedDeps(ctx, cfg, db, l)
		if err != nil {
			l.Fatal("failed to build shared deps", zap.Error(err))
		}

		// Wire the handler. Lookup uses the data-plane object repo
		// so we can resolve (tenant, object_key, key) → object_id;
		// statemachine.Transitioner already lives on SharedDeps.
		handler := &eventingest.PromoteHandler{
			Lookup:       db.Queries,
			Transitioner: deps.SM,
			Logger:       l.Named("ingest.handler"),
		}

		// Build the configured driver.
		driver, err := buildIngestDriver(cfg.Ingest, l)
		if err != nil {
			l.Fatal("failed to build ingest driver", zap.Error(err))
		}

		worker := &eventingest.Worker{
			Driver:  driver,
			Handler: handler,
			Dedup:   &eventingest.PgxDedupStore{Q: db.Queries},
			Logger:  l.Named("ingest.worker"),
		}

		// Reaper runs alongside the worker — keeps the dedup table
		// bounded. Cheap enough that we don't need a separate pod
		// for it.
		reaper := &eventingest.Reaper{
			Q:        db.Queries,
			Interval: cfg.Ingest.ReaperInterval,
			TTL:      cfg.Ingest.DedupTTL,
			Logger:   l.Named("ingest.reaper"),
		}
		go func() {
			if err := reaper.Run(ctx); err != nil && !errorsIsCancelled(err) {
				l.Warn("ingest reaper exited", zap.Error(err))
			}
		}()

		// Health server. The webhook driver binds its own listener on
		// cfg.Ingest.Webhook.Addr (which serves /healthz alongside the
		// receiver routes), but nats / rabbitmq drivers have nothing
		// HTTP-shaped — without an ops endpoint kubelet's liveness
		// probe sees ECONNREFUSED on :8100 and crash-loops the pod
		// every 60s, AND the BFF /api/health/all aggregator gets no
		// /system/health.json snapshot to render on the operator
		// /health page.
		//
		// Wire the same `app.NewHealthHandler` mux the worker /
		// dispatcher / api / admin pods serve, with one
		// driver-specific subsystem check (subscriber connectivity).
		// Same pattern as serve_dispatcher.go::dispatcherOpsMux.
		if cfg.Ingest.Driver != "webhook" && cfg.Ingest.Webhook.Addr != "" {
			go runIngestOpsServer(ctx, cfg.Ingest.Webhook.Addr, deps, driver, l)
		}

		l.Info("ingest plane starting", zap.String("driver", cfg.Ingest.Driver))
		if err := worker.Run(ctx); err != nil && !errorsIsCancelled(err) {
			l.Error("ingest worker exited", zap.Error(err))
		}
	},
}

// runIngestOpsServer mounts the same kind of ops mux the other worker
// pods serve: health.Handler.Register adds /healthz + /readyz +
// /startupz + /system/health.json, plus our subscriber subsystem
// check. The check is driver-aware:
//
//   - NATS driver: the underlying *nats.Conn must be CONNECTED.
//     Required, so a wedged broker fails /readyz and ArgoCD / kubelet
//     react instead of silently dropping events.
//
//   - Other drivers (rabbitmq today, possibly others later): we
//     can't introspect them with the same shape, so we skip the
//     subscriber check rather than ship a placeholder that's
//     always healthy. Postgres + the shared default checks still
//     run.
func runIngestOpsServer(ctx context.Context, addr string, deps *app.SharedDeps, drv eventingest.Driver, l *zap.Logger) {
	healthH := app.NewHealthHandler(deps.DB, deps.Cfg.Runtime, l).WithRole("ingest")

	if natsDrv, ok := drv.(*eventingest.NATSDriver); ok {
		app.AddSubsystemCheck(healthH, "subscriber", true, func(ctx context.Context) error {
			if st := natsDrv.Status(); st != nats.CONNECTED {
				return fmt.Errorf("nats subscriber: status=%s", st)
			}
			return nil
		})
	}

	mux := http.NewServeMux()
	healthH.Register(mux)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	l.Info("ingest ops listener", zap.String("addr", addr))
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		l.Warn("ingest ops listener exited", zap.Error(err))
	}
}

// buildIngestDriver selects the transport based on cfg.Ingest.Driver
// and constructs it with its source adapter(s). Returns an error
// rather than logger.Fatal so the caller decides how to surface it.
func buildIngestDriver(cfg config.Ingest, l *zap.Logger) (eventingest.Driver, error) {
	switch cfg.Driver {
	case "webhook":
		return buildWebhookDriver(cfg, l)
	case "nats":
		return buildNATSDriver(cfg, l)
	case "rabbitmq":
		return buildRabbitMQDriver(cfg, l)
	default:
		return nil, fmt.Errorf("ingest: unknown driver %q (expected webhook | nats | rabbitmq)", cfg.Driver)
	}
}

// buildNATSDriver wires the NATS subscriber. Source format selects
// which adapter parses each message — there's no per-subject routing
// like the webhook driver does, so the binding is fixed at boot.
func buildNATSDriver(cfg config.Ingest, l *zap.Logger) (eventingest.Driver, error) {
	if cfg.NATS.URL == "" {
		return nil, fmt.Errorf("ingest: nats.url required when driver=nats")
	}
	src, err := pickSource(cfg.NATS.SourceFormat)
	if err != nil {
		return nil, err
	}
	return &eventingest.NATSDriver{
		URL:         cfg.NATS.URL,
		Subject:     cfg.NATS.Subject,
		QueueGroup:  cfg.NATS.QueueGroup,
		JetStream:   cfg.NATS.JetStream,
		DurableName: cfg.NATS.DurableName,
		Token:       cfg.NATS.Token,
		SourceAdapt: src,
		Logger:      l.Named("ingest.nats"),
	}, nil
}

// buildRabbitMQDriver wires the AMQP consumer. Operator-declared queue
// is consumed at the configured prefetch.
func buildRabbitMQDriver(cfg config.Ingest, l *zap.Logger) (eventingest.Driver, error) {
	if cfg.RabbitMQ.URL == "" {
		return nil, fmt.Errorf("ingest: rabbitmq.url required when driver=rabbitmq")
	}
	if cfg.RabbitMQ.Queue == "" {
		return nil, fmt.Errorf("ingest: rabbitmq.queue required when driver=rabbitmq")
	}
	src, err := pickSource(cfg.RabbitMQ.SourceFormat)
	if err != nil {
		return nil, err
	}
	return &eventingest.RabbitMQDriver{
		URL:           cfg.RabbitMQ.URL,
		Queue:         cfg.RabbitMQ.Queue,
		PrefetchCount: cfg.RabbitMQ.PrefetchCount,
		SourceAdapt:   src,
		Logger:        l.Named("ingest.rabbitmq"),
	}, nil
}

// pickSource resolves a source-format string to the matching adapter.
// Webhook driver picks per-route via its Sources map; NATS / RabbitMQ
// drivers pick once via this helper.
//
// `seaweedfs` parses the JSON shape SeaweedFS' `[notification.webhook]`
// driver emits — operators using the webhook publisher.
//
// `seaweedfs_nats` parses the gob-encoded gocdk_pub_sub envelope SF
// emits when configured with `[notification.gocdk_pub_sub]
// topic_url = nats://...`. This is what the in-cluster setup uses
// (see gitops/.../seaweedfs/notification-config.yaml). The two
// formats are NOT interchangeable — picking the wrong one produces
// `ErrUnrecognisedEvent` on every message and the dedup table fills
// with junk.
//
// BucketName for the SeaweedFS sources is hard-coded to "paladin-primary"
// to match cfg.Storage.DefaultBackend in the local overlay. When
// the operator's bucket name diverges this should be read from
// cfg.Storage.Backends; threading that through is BACKLOG'd under
// "Storage event ingest pipeline" since the producer adapter and
// the storage config are wired by separate teams.
func pickSource(format string) (eventingest.Source, error) {
	switch format {
	case "seaweedfs":
		return &eventingest.SeaweedFSSource{
			BucketName: "paladin-primary",
			URI:        "seaweedfs://primary",
		}, nil
	case "seaweedfs_nats":
		return &eventingest.SeaweedFSNATSSource{
			BucketName: "paladin-primary",
			URI:        "seaweedfs-nats://primary",
		}, nil
	case "minio":
		return &eventingest.MinIOSource{URI: "minio://primary"}, nil
	case "cloudevents":
		return &eventingest.CloudEventsSource{URI: "cloudevents://primary"}, nil
	case "":
		return nil, fmt.Errorf("ingest: source_format required (seaweedfs | seaweedfs_nats | minio | cloudevents)")
	default:
		return nil, fmt.Errorf("ingest: unknown source_format %q", format)
	}
}

// buildWebhookDriver wires the receiver mux. SeaweedFS source adapter
// is registered when at least one storage backend is configured —
// the bucket name is taken from cfg.Storage.DefaultBackend.
//
// CloudEvents passthrough endpoint is always available so operators
// can use PALADIN's ingest plane as a generic CloudEvents 1.0 sink during
// integration without a custom adapter.
func buildWebhookDriver(cfg config.Ingest, l *zap.Logger) (eventingest.Driver, error) {
	sources := map[string]eventingest.Source{}

	// SeaweedFS adapter — operator gives us a bucket name implicitly
	// via cfg.Storage.DefaultBackend.bucket. We can't reach that here
	// without threading config.Storage in; the simpler path is to
	// require the operator to set the bucket explicitly for the
	// adapter via the webhook URL prefix in their notification.toml,
	// which we tolerate (no bucket = no bucket prefix to strip).
	//
	// For day-1 we register the adapter with empty BucketName; the
	// path parser still works as long as the publisher sends a
	// 3-component path "<tenant>/<object_key>/<key>" without a
	// bucket prefix — the natural shape if SeaweedFS is configured
	// with `notification.webhook.endpoint = .../webhook/seaweedfs`.
	sources["/webhook/seaweedfs"] = &eventingest.SeaweedFSSource{
		BucketName: "",
		URI:        "seaweedfs://primary",
	}

	// MinIO bucket-notifications POST to /webhook/minio with the
	// AWS S3 event-notification JSON envelope. BucketName is empty
	// so the adapter accepts every bucket; a multi-bucket deploy
	// can split into per-route adapters later.
	sources["/webhook/minio"] = &eventingest.MinIOSource{
		BucketName: "",
		URI:        "minio://primary",
	}

	// Vendor-neutral CloudEvents 1.0 endpoint. Any publisher that
	// can post a structured-mode CE envelope reaches the ingest
	// plane through this route — no per-vendor adapter needed.
	sources["/webhook/cloudevents"] = &eventingest.CloudEventsSource{
		URI: "cloudevents://primary",
	}

	return &eventingest.WebhookDriver{
		Addr:           cfg.Webhook.Addr,
		Sources:        sources,
		SharedSecret:   cfg.Webhook.SharedSecret,
		SignatureHdr:   cfg.Webhook.SignatureHeader,
		MaxBodyBytes:   cfg.Webhook.MaxBodyBytes,
		ReadHdrTimeout: cfg.Webhook.ReadHeaderTimeout,
		ReadTimeout:    cfg.Webhook.ReadTimeout,
		WriteTimeout:   cfg.Webhook.WriteTimeout,
		IdleTimeout:    cfg.Webhook.IdleTimeout,
		Logger:         l.Named("ingest.webhook"),
	}, nil
}

// silenced unused import — keep adapters import compiling for future
// growth (handler uses sqlc.Queries directly today).
var _ = adapters.NewObjectRepo

// silence ctx-typed lint while context import is the only spot needed.
var _ = context.Background
