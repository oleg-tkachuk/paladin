package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/spf13/cobra"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/app"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/eventingest"
	"github.com/oleg-tkachuk/paladin/backend/internal/observability"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
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
		fx.New(
			fx.Supply(configSource()),
			fx.Supply(buildMeta()),
			ingestModule,
		).Run()
	},
}

// ingestModule is the ingest role's fx graph: the DB-backed BaseModule plus the
// storage-event consumer lifecycle. Extracted so both the command and the
// graph-validation test (fx_validate_test.go) reference the same wiring.
var ingestModule = fx.Options(
	app.BaseModule,
	fx.Invoke(runIngest),
)

// runIngest is the ingest role's fx lifecycle. It validates the subsystem is
// enabled, builds the driver/handler/worker/reaper and the optional BYPASSRLS
// pool up front (any failure aborts start, matching the pre-fx Fatal), then
// OnStart spawns the reaper, the driver-aware ops listener, and the ingest
// worker; a non-recoverable worker error escalates to an exit-1 shutdown. OnStop
// stops the loop, then closes the own pool + shared DB and flushes OTel — the
// same teardown the pre-fx defers produced.
func runIngest(
	lc fx.Lifecycle,
	sd fx.Shutdowner,
	cfg config.Config,
	l *zap.Logger,
	db *postgres.DB,
	otel observability.ShutdownFunc,
	deps *app.SharedDeps,
) error {
	if !cfg.Ingest.Enabled {
		return fmt.Errorf("cfg.Ingest.Enabled=false; refuse to start serve ingest with the subsystem disabled")
	}

	// Lookup + state-machine transitions for the ingest worker
	// MUST run cross-tenant — the storage event arrives via NATS
	// from SF without any auth context, so the runtime pool's
	// RLS GUC is empty and `paladin_app` filters every row out.
	// Same pattern as serve_dispatcher.go: open a dedicated pool
	// from cfg.Datastores.Postgres.MigrateDSN (BYPASSRLS).
	// Without this the smoke logs "no matching object for event;
	// skipping" on every PUT — looks like a race / ordering bug,
	// is actually RLS denying the SELECT.
	ingestQueries := db.Queries
	ingestSM := deps.SM
	ingestPool := deps.Pool
	var ownPool *pgxpool.Pool // non-nil only when we opened a dedicated BYPASSRLS pool
	// The ingest Lookup + state-machine transitions run cross-tenant (pure DML),
	// so the BYPASSRLS pool uses the least-privilege paladin_reaper role (reaper_dsn),
	// falling back to paladin_migrate when unset (dev parity). See `002_roles_and_rls.sql`.
	if bypassDSN, bypassPwd := bypassRLSConn(cfg); bypassDSN != "" {
		pool, err := newDispatcherPool(
			context.Background(),
			bypassDSN,
			bypassPwd,
			"paladin-ingest",
			l,
		)
		if err != nil {
			return fmt.Errorf("open ingest pool: %w", err)
		}
		ownPool = pool
		ingestQueries = sqlc.New(pool)
		ingestSM = statemachine.New(pool)
		ingestPool = pool
	} else {
		l.Warn("ingest: MigrateDSN not set; using runtime pool — " +
			"RLS will gate the lookup and PROMOTE will silently no-op " +
			"for every event. Set datastores.postgres.migrate_dsn to a " +
			"BYPASSRLS role.")
	}

	// Wire the handler. Lookup uses the data-plane object repo
	// so we can resolve (tenant, collection, key) → object_id;
	// statemachine.Transitioner already lives on SharedDeps but
	// we replace it with one bound to ingestQueries so PROMOTE
	// flows through the same BYPASSRLS pool.
	// Producer-only dispatcher: enqueues paladin.object.uploaded outbox
	// rows on the promote tx (ADR-0003) so an explicit-mode storage
	// event notifies webhook subscribers just like a CompleteObject
	// RPC does. Bound to the ingest (BYPASSRLS) queries so the sub
	// fan-out isn't RLS-gated. The separate `serve dispatcher` pod
	// drains event_deliveries and does the actual delivery, so NATS
	// stays nil here — enqueue never opens a socket.
	ingestDispatcher := &worker.Dispatcher{
		Store:       worker.NewRepoSubscriptionStore(adapters.NewEventSubscriptionRepoV2(ingestQueries)),
		Outbox:      worker.PgxOutboxWriter{Pool: ingestPool},
		Logger:      l.Named("ingest-event-dispatcher"),
		MaxAttempts: 3,
	}

	handler := &eventingest.PromoteHandler{
		// Wrap the queries in the per-tenant longest-prefix cache so
		// ResolveCollectionPrefix isn't a SQL round-trip on every storage
		// event (bounded-staleness — see eventingest.CachingLookup).
		Lookup:       eventingest.NewCachingLookup(ingestQueries, eventingest.DefaultPrefixCacheTTL, l.Named("ingest.prefix-cache")),
		Transitioner: ingestSM,
		Events:       ingestDispatcher,
		Logger:       l.Named("ingest.handler"),
	}

	// Build the configured driver.
	driver, err := buildIngestDriver(cfg.Ingest, l)
	if err != nil {
		return fmt.Errorf("build ingest driver: %w", err)
	}

	ingestWorker := &eventingest.Worker{
		Driver:  driver,
		Handler: handler,
		// Dedup shares the ingest (BYPASSRLS) queries, not the runtime
		// RLS pool: ingest events carry no tenant GUC, so on the RLS
		// pool a future RLS policy on ingest_events would silently make
		// every dedup check miss and re-process every event.
		Dedup:  &eventingest.PgxDedupStore{Q: ingestQueries},
		Logger: l.Named("ingest.worker"),
	}

	// Reaper runs alongside the worker — keeps the dedup table
	// bounded. Cheap enough that we don't need a separate pod for it.
	reaper := &eventingest.Reaper{
		Q:        db.Queries,
		Interval: cfg.Ingest.ReaperInterval,
		TTL:      cfg.Ingest.DedupTTL,
		Logger:   l.Named("ingest.reaper"),
	}

	// workCtx bounds the worker, reaper, and ops listener; cancelled OnStop.
	// workerDone closes when ingestWorker.Run returns, so OnStop can wait for
	// the in-flight event to drain before closing the pools.
	workCtx, cancel := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	var metricsLn *app.MetricsListener

	lc.Append(fx.Hook{
		OnStart: func(startCtx context.Context) error {
			ml, err := app.StartMetricsListener(startCtx, deps, l)
			if err != nil {
				return err
			}
			metricsLn = ml
			go func() {
				if err := reaper.Run(workCtx); err != nil && !errorsIsCancelled(err) {
					l.Warn("ingest reaper exited", zap.Error(err))
				}
			}()

			// Health server. The webhook driver binds its own listener on
			// cfg.Ingest.Webhook.Addr (which serves /healthz alongside the
			// receiver routes), but nats / rabbitmq drivers have nothing
			// HTTP-shaped — without an ops endpoint kubelet's liveness probe
			// sees ECONNREFUSED on :8100 and crash-loops the pod every 60s,
			// AND the BFF /api/health/all aggregator gets no
			// /system/health.json snapshot to render on the operator /health
			// page. Same app.NewHealthHandler mux the other pods serve, with
			// one driver-specific subscriber check. runIngestOpsServer watches
			// workCtx.Done() and shuts its listener down on cancel.
			if cfg.Ingest.Driver != "webhook" && cfg.Ingest.Webhook.Addr != "" {
				go runIngestOpsServer(workCtx, cfg.Ingest.Webhook.Addr, deps, driver, l)
			}

			l.Info("ingest plane starting", zap.String("driver", cfg.Ingest.Driver))
			go func() {
				err := ingestWorker.Run(workCtx)
				close(workerDone)
				if err != nil && !errorsIsCancelled(err) {
					// Non-recoverable (config / subscribe / stream-missing) —
					// the NATS driver already retries the initial dial in the
					// background, so reaching here is fatal. Escalate to an
					// exit-1 fx shutdown so kubelet reports Reason: Error and
					// applies CrashLoopBackOff rather than masking the failure.
					l.Error("ingest worker exited", zap.Error(err))
					_ = sd.Shutdown(fx.ExitCode(1))
				}
			}()
			return nil
		},
		OnStop: func(context.Context) error {
			cancel() // stop the worker, reaper, and ops listener
			<-workerDone
			shutdownCtx, c := context.WithTimeout(context.Background(), defaultShutdownGrace)
			defer c()
			_ = metricsLn.Shutdown(shutdownCtx)
			if ownPool != nil {
				ownPool.Close()
			}
			// Bounded (5s) fresh-context OTel flush — the fx OnStop context
			// carries the 90s StopTimeout, so a slow/unreachable OTLP endpoint
			// would otherwise block teardown past the pod's termination grace
			// and get SIGKILLed mid-shutdown.
			flushOTel(otel)
			deps.StopWatchers() // release the Cedar LISTEN conn before pool close
			db.Close()
			_ = l.Sync()
			return nil
		},
	})
	return nil
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
	srv := &http.Server{
		Addr:              addr,
		Handler:           ingestOpsMux(deps, drv, l),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { // #nosec G118 -- detached ctx is intentional; the parent ctx is already canceled at shutdown time
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

// ingestOpsMux is the ops surface runIngestOpsServer serves: the health
// endpoints and the driver's subscriber check.
func ingestOpsMux(deps *app.SharedDeps, drv eventingest.Driver, l *zap.Logger) http.Handler {
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

	// No /metrics here: it is served by app.StartMetricsListener on
	// otel.metrics_addr, the one scrape port every role shares (ADR-0023).
	// This listener runs only for the nats and rabbitmq drivers, so the
	// webhook driver had no scrape endpoint while /metrics lived here.

	// Backwards-compat alias — chart probes hit `/healthz` (the
	// historical Kubernetes path), but health.Handler.Register
	// mounts `/livez` (the current convention). Same patch the
	// dispatcher pod applies; without this the pod readiness flips
	// to false on a 404 and kubelet crash-loops it every 60s.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/livez"
		mux.ServeHTTP(w, r2)
	})
	return mux
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
	case "sqs":
		return buildSQSDriver(cfg, l)
	default:
		return nil, fmt.Errorf("ingest: unknown driver %q (expected webhook | nats | rabbitmq | sqs)", cfg.Driver)
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

// buildSQSDriver wires the AWS SQS poller. The queue (with its S3 notification
// + optional redrive policy) is declared out-of-band; this driver only
// receives + deletes. source_format defaults to "s3" — the format S3 emits.
func buildSQSDriver(cfg config.Ingest, l *zap.Logger) (eventingest.Driver, error) {
	sc := cfg.SQS
	if sc.QueueURL == "" {
		return nil, fmt.Errorf("ingest: sqs.queue_url required when driver=sqs")
	}
	if sc.Region == "" {
		return nil, fmt.Errorf("ingest: sqs.region required when driver=sqs")
	}
	format := sc.SourceFormat
	if format == "" {
		format = "s3"
	}
	src, err := pickSource(format)
	if err != nil {
		return nil, err
	}
	client, err := newSQSReceiveClient(context.Background(), sc)
	if err != nil {
		return nil, fmt.Errorf("ingest: build sqs client: %w", err)
	}
	return &eventingest.SQSDriver{
		Client:            client,
		QueueURL:          sc.QueueURL,
		MaxMessages:       sc.MaxMessages,
		WaitTimeSeconds:   sc.WaitTimeSeconds,
		VisibilityTimeout: sc.VisibilityTimeout,
		UnwrapSNS:         sc.UnwrapSNS,
		SourceAdapt:       src,
		Logger:            l.Named("ingest.sqs"),
	}, nil
}

// newSQSReceiveClient resolves AWS config (credential chain + region),
// optionally assuming a cross-account role and/or pointing at a custom
// endpoint (LocalStack / tests), and returns a live SQS client.
func newSQSReceiveClient(ctx context.Context, sc config.IngestSQS) (eventingest.SQSReceiver, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(sc.Region))
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	if sc.RoleArn != "" {
		stsClient := sts.NewFromConfig(awsCfg)
		awsCfg.Credentials = aws.NewCredentialsCache(
			stscreds.NewAssumeRoleProvider(stsClient, sc.RoleArn))
	}
	var sqsOpts []func(*sqs.Options)
	if sc.Endpoint != "" {
		sqsOpts = append(sqsOpts, func(o *sqs.Options) {
			o.BaseEndpoint = aws.String(sc.Endpoint)
		})
	}
	return sqs.NewFromConfig(awsCfg, sqsOpts...), nil
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
	case "s3":
		// Generic AWS-S3 event-notification JSON — AWS S3, or any
		// S3-compatible store that emits bucket notifications.
		return &eventingest.S3EventSource{URI: "s3://primary", Label: "s3"}, nil
	case "minio":
		// MinIO speaks the same S3 event format; distinct label so
		// metrics/logs attribute it to MinIO.
		return &eventingest.S3EventSource{URI: "minio://primary", Label: "minio"}, nil
	case "cloudevents":
		return &eventingest.CloudEventsSource{URI: "cloudevents://primary"}, nil
	case "garage":
		// Garage has NO event-notification capability: Get/PutBucket
		// NotificationConfiguration are 501 Not Implemented and it exposes
		// no non-S3 event/webhook mechanism either. Fail loudly rather than
		// silently subscribing to a source that will never publish. The
		// Reconciler covers direct-to-Garage writes; see docs/storage-ingest.md.
		return nil, fmt.Errorf(
			"ingest: source_format %q is not supported (Garage emits no notifications)", format)
	case "":
		return nil, fmt.Errorf("ingest: source_format required (seaweedfs | seaweedfs_nats | s3 | minio | cloudevents)")
	default:
		return nil, fmt.Errorf("ingest: unknown source_format %q", format)
	}
}

// buildWebhookDriver wires the receiver mux. SeaweedFS source adapter
// is registered when at least one storage backend is configured —
// the bucket name is taken from cfg.Storage.DefaultBackend.
//
// CloudEvents passthrough endpoint is always available so operators
// can use Paladin's ingest plane as a generic CloudEvents 1.0 sink during
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
	// 3-component path "<tenant>/<collection>/<key>" without a
	// bucket prefix — the natural shape if SeaweedFS is configured
	// with `notification.webhook.endpoint = .../webhook/seaweedfs`.
	sources["/webhook/seaweedfs"] = &eventingest.SeaweedFSSource{
		BucketName: "",
		URI:        "seaweedfs://primary",
	}

	// S3 bucket-notifications POST the AWS S3 event-notification JSON
	// envelope. `/webhook/s3` is the vendor-neutral route (AWS S3 via
	// SNS→HTTPS, or any S3-compatible store); `/webhook/minio` is the same
	// parser with a MinIO label, kept for operators who already point MinIO
	// at it. BucketName empty → accept every bucket (a multi-bucket deploy
	// can split into per-route adapters later).
	sources["/webhook/s3"] = &eventingest.S3EventSource{
		BucketName: "",
		URI:        "s3://primary",
		Label:      "s3",
	}
	sources["/webhook/minio"] = &eventingest.S3EventSource{
		BucketName: "",
		URI:        "minio://primary",
		Label:      "minio",
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
