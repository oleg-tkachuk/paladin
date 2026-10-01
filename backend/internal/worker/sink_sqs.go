package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
)

// sqsSender is the subset of the AWS SQS client the sink uses, narrowed so
// tests substitute a fake without the SDK or a live queue.
type sqsSender interface {
	SendMessage(ctx context.Context, in *sqs.SendMessageInput, optFns ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
	SendMessageBatch(ctx context.Context, in *sqs.SendMessageBatchInput, optFns ...func(*sqs.Options)) (*sqs.SendMessageBatchOutput, error)
}

// sqsSinkConfig is the JSON shape stored in event_subscriptions.sink_config
// for sink_kind='sqs'. Tags match the generated pb.SqsSink struct so the
// connectshim round-trip (sinkToConfig → JSON → here) lines up.
type sqsSinkConfig struct {
	QueueURL string `json:"queue_url"`
	Region   string `json:"region"`
	// RoleArn, when set, is sts:AssumeRole'd before delivery for cross-account
	// queues. Empty = ambient credentials (same-account).
	RoleArn string `json:"role_arn"`
}

// SQSClientPool lazily builds + caches one SQS client per (region, roleArn).
// Mirrors NatsConnPool's contract: empty until the first sqs-sink delivery,
// safe for concurrent use, owned by the dispatcher pod. There are no sockets
// to close (the SDK clients are stateless HTTP), so it needs no Close().
//
// Keying by (region, roleArn) means a same-account sink and a cross-account
// sink to the same region get distinct cached clients — each with its own
// (possibly AssumeRole'd) credential provider.
type SQSClientPool struct {
	mu      sync.Mutex
	clients map[string]sqsSender
	log     *zap.Logger
	// newClient builds a region-bound client, assuming roleArn when non-empty.
	// Overridable in tests so the pool can hand back a fake without touching
	// AWS credential resolution.
	newClient func(ctx context.Context, region, roleArn string) (sqsSender, error)
}

// NewSQSClientPool returns an empty pool whose clients resolve AWS config
// (credentials chain, region) on first use per (region, roleArn).
func NewSQSClientPool(log *zap.Logger) *SQSClientPool {
	return &SQSClientPool{
		clients: map[string]sqsSender{},
		log:     log,
		newClient: func(ctx context.Context, region, roleArn string) (sqsSender, error) {
			cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
			if err != nil {
				return nil, fmt.Errorf("load aws config: %w", err)
			}
			if roleArn != "" {
				// Cross-account: assume the target role off the ambient
				// (IRSA / env) credentials. Cached so the STS AssumeRole is
				// re-called only when the SDK refreshes the ~1h credentials.
				stsClient := sts.NewFromConfig(cfg)
				cfg.Credentials = aws.NewCredentialsCache(
					stscreds.NewAssumeRoleProvider(stsClient, roleArn))
			}
			return sqs.NewFromConfig(cfg), nil
		},
	}
}

// clientKey namespaces the cache by region AND assumed role so a same-account
// and a cross-account sink to one region don't share a client.
func clientKey(region, roleArn string) string { return region + "\x00" + roleArn }

func (p *SQSClientPool) get(ctx context.Context, region, roleArn string) (sqsSender, error) {
	key := clientKey(region, roleArn)
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.clients[key]; ok {
		return c, nil
	}
	c, err := p.newClient(ctx, region, roleArn)
	if err != nil {
		return nil, err
	}
	p.clients[key] = c
	if p.log != nil {
		p.log.Info("sqs client built", zap.String("region", region), zap.Bool("assume_role", roleArn != ""))
	}
	return c, nil
}

// deliverSQS publishes the CloudEvents 1.0 envelope to the SQS queue
// configured on the subscription. Success means SendMessage returned no
// error (SQS persists + acks synchronously, so unlike the fire-and-forget
// NATS sink there is a real broker ack here). statusCode is 0 — non-HTTP
// sinks report 0 so the outbox/UI treat failures like transport errors.
func (d *Dispatcher) deliverSQS(ctx context.Context, sub admindomain.EventSubscription, evt Event) (int, error) {
	if d.SQS == nil {
		return 0, errors.New("sqs sink: dispatcher has no SQS client pool")
	}
	var cfg sqsSinkConfig
	if err := json.Unmarshal(sub.SinkConfig, &cfg); err != nil {
		return 0, fmt.Errorf("sqs sink: decode config: %w", err)
	}
	if cfg.QueueURL == "" {
		return 0, errors.New("sqs sink: missing queue_url")
	}
	if cfg.Region == "" {
		return 0, errors.New("sqs sink: missing region")
	}
	body, groupID, dedupID, err := d.buildSQSMessage(cfg, sub, evt)
	if err != nil {
		return 0, err
	}

	in := &sqs.SendMessageInput{
		QueueUrl:               aws.String(cfg.QueueURL),
		MessageBody:            aws.String(body),
		MessageGroupId:         groupID,
		MessageDeduplicationId: dedupID,
	}

	client, err := d.SQS.get(ctx, cfg.Region, cfg.RoleArn)
	if err != nil {
		return 0, err
	}
	if _, err := client.SendMessage(ctx, in); err != nil {
		return 0, fmt.Errorf("sqs send: %w", err)
	}
	return 0, nil
}

// buildSQSMessage renders the CloudEvents body plus the FIFO attributes
// shared by the single-send and batch paths. FIFO queues (URL suffix
// ".fifo") REQUIRE a MessageGroupId and, with content-based dedup off, a
// MessageDeduplicationId: group per tenant so a tenant's events stay
// ordered; dedup on the (stable) delivery-row id so a retried row is
// collapsed by SQS rather than double-delivered.
func (d *Dispatcher) buildSQSMessage(cfg sqsSinkConfig, sub admindomain.EventSubscription, evt Event) (body string, groupID, dedupID *string, err error) {
	raw, err := json.Marshal(d.newCloudEventEnvelope(sub, evt))
	if err != nil {
		return "", nil, nil, fmt.Errorf("sqs sink: marshal envelope: %w", err)
	}
	if strings.HasSuffix(cfg.QueueURL, ".fifo") {
		dedup := evt.ID
		if dedup == "" {
			dedup = sub.SubscriptionID.String()
		}
		groupID = aws.String(evt.TenantID)
		dedupID = aws.String(dedup)
	}
	return string(raw), groupID, dedupID, nil
}

// ─── Batched delivery (outbox fan-in) ────────────────────────────────────────

// SQS batch limits: 10 entries per SendMessageBatch call, 256 KiB summed
// payload. We chunk under both, with headroom on the byte budget for the
// per-entry attribute overhead the sum doesn't count.
const (
	sqsMaxBatchEntries = 10
	sqsMaxBatchBytes   = 240 * 1024
)

// sqsBatchItem is one outbox row headed for a shared SQS queue.
type sqsBatchItem struct {
	RowID uuid.UUID // delivery-row id: result mapping + FIFO dedup
	Sub   admindomain.EventSubscription
	Evt   Event
}

// sqsGroupTarget derives the batch-group key for a subscription IFF it is a
// well-formed SQS sink. Rows whose config doesn't parse (or lacks the
// required fields) return ok=false and take the per-row deliver path, so
// they fail with exactly the same error text as before batching existed.
func sqsGroupTarget(sub admindomain.EventSubscription) (key string, cfg sqsSinkConfig, ok bool) {
	if sub.SinkKind != "sqs" {
		return "", cfg, false
	}
	if err := json.Unmarshal(sub.SinkConfig, &cfg); err != nil {
		return "", cfg, false
	}
	if cfg.QueueURL == "" || cfg.Region == "" {
		return "", cfg, false
	}
	return cfg.QueueURL + "\x00" + cfg.Region + "\x00" + cfg.RoleArn, cfg, true
}

// deliverSQSBatch sends one group's rows via SendMessageBatch, chunked under
// the entry/byte limits, and returns a per-row outcome (nil = delivered).
// A whole-call failure marks every row of that chunk failed (retryable);
// a partial failure maps each BatchResultErrorEntry back to its row. Rows
// therefore keep their individual attempts/backoff/permanent bookkeeping —
// batching changes the transport, not the outbox contract.
func (d *Dispatcher) deliverSQSBatch(ctx context.Context, cfg sqsSinkConfig, items []sqsBatchItem) map[uuid.UUID]error {
	out := make(map[uuid.UUID]error, len(items))
	if d.SQS == nil {
		for _, it := range items {
			out[it.RowID] = errors.New("sqs sink: dispatcher has no SQS client pool")
		}
		return out
	}
	client, err := d.SQS.get(ctx, cfg.Region, cfg.RoleArn)
	if err != nil {
		for _, it := range items {
			out[it.RowID] = err
		}
		return out
	}

	type entry struct {
		rowID uuid.UUID
		in    sqstypes.SendMessageBatchRequestEntry
		size  int
	}
	var pendingEntries []entry
	for _, it := range items {
		body, groupID, dedupID, berr := d.buildSQSMessage(cfg, it.Sub, it.Evt)
		if berr != nil {
			out[it.RowID] = berr
			continue
		}
		pendingEntries = append(pendingEntries, entry{
			rowID: it.RowID,
			in: sqstypes.SendMessageBatchRequestEntry{
				Id:                     aws.String(it.RowID.String()),
				MessageBody:            aws.String(body),
				MessageGroupId:         groupID,
				MessageDeduplicationId: dedupID,
			},
			size: len(body),
		})
	}

	flush := func(chunk []entry) {
		if len(chunk) == 0 {
			return
		}
		byID := make(map[string]uuid.UUID, len(chunk))
		in := &sqs.SendMessageBatchInput{QueueUrl: aws.String(cfg.QueueURL)}
		for _, e := range chunk {
			byID[*e.in.Id] = e.rowID
			in.Entries = append(in.Entries, e.in)
		}
		resp, err := client.SendMessageBatch(ctx, in)
		if err != nil {
			for _, e := range chunk {
				out[e.rowID] = fmt.Errorf("sqs batch send: %w", err)
			}
			return
		}
		for _, ok := range resp.Successful {
			if ok.Id != nil {
				out[byID[*ok.Id]] = nil
			}
		}
		for _, f := range resp.Failed {
			if f.Id == nil {
				continue
			}
			code, msg := aws.ToString(f.Code), aws.ToString(f.Message)
			out[byID[*f.Id]] = fmt.Errorf("sqs batch entry failed: %s: %s", code, msg)
		}
	}

	var chunk []entry
	var chunkBytes int
	for _, e := range pendingEntries {
		if len(chunk) > 0 && (len(chunk) >= sqsMaxBatchEntries || chunkBytes+e.size > sqsMaxBatchBytes) {
			flush(chunk)
			chunk, chunkBytes = nil, 0
		}
		chunk = append(chunk, e)
		chunkBytes += e.size
	}
	flush(chunk)
	return out
}
