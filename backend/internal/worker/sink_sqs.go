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
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
)

// sqsSender is the subset of the AWS SQS client the sink uses. Narrowed (the
// same trick as internal/storage/events.SQSClient) so tests substitute a fake
// without the SDK or a live queue.
type sqsSender interface {
	SendMessage(ctx context.Context, in *sqs.SendMessageInput, optFns ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
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
	body, err := json.Marshal(d.newCloudEventEnvelope(sub, evt))
	if err != nil {
		return 0, fmt.Errorf("sqs sink: marshal envelope: %w", err)
	}

	in := &sqs.SendMessageInput{
		QueueUrl:    aws.String(cfg.QueueURL),
		MessageBody: aws.String(string(body)),
	}
	// FIFO queues (URL suffix ".fifo") REQUIRE a MessageGroupId and, with
	// content-based dedup off, a MessageDeduplicationId. Group per tenant so
	// a tenant's events stay ordered; dedup on the (stable) delivery-row id
	// so a retried row is collapsed by SQS rather than double-delivered.
	if strings.HasSuffix(cfg.QueueURL, ".fifo") {
		dedup := evt.ID
		if dedup == "" {
			dedup = sub.SubscriptionID.String()
		}
		in.MessageGroupId = aws.String(evt.TenantID)
		in.MessageDeduplicationId = aws.String(dedup)
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
