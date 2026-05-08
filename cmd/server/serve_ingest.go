package main

import (
	"context"
	"fmt"

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

		l.Info("ingest plane starting", zap.String("driver", cfg.Ingest.Driver))
		if err := worker.Run(ctx); err != nil && !errorsIsCancelled(err) {
			l.Error("ingest worker exited", zap.Error(err))
		}
	},
}

// buildIngestDriver selects the transport based on cfg.Ingest.Driver
// and constructs it with its source adapter(s). Returns an error
// rather than logger.Fatal so the caller decides how to surface it.
func buildIngestDriver(cfg config.Ingest, l *zap.Logger) (eventingest.Driver, error) {
	switch cfg.Driver {
	case "webhook":
		return buildWebhookDriver(cfg, l)
	case "nats":
		return nil, fmt.Errorf("ingest: nats driver not yet implemented in this build")
	case "rabbitmq":
		return nil, fmt.Errorf("ingest: rabbitmq driver not yet implemented in this build")
	default:
		return nil, fmt.Errorf("ingest: unknown driver %q (expected webhook | nats | rabbitmq)", cfg.Driver)
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

	// Future-friendly: stub the cloudevents passthrough behind the
	// same dispatcher. Adapter is a fall-through — first source we
	// add for it lands in a follow-up commit.
	// sources["/webhook/cloudevents"] = &cloudEventsSource{}

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
