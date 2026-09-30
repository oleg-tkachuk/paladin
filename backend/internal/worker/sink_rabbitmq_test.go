package worker

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
)

type rabbitCall struct {
	exchange, routingKey string
	body                 []byte
}

type fakeRabbit struct {
	calls     []rabbitCall
	err       error
	isHealthy bool
	closed    int
}

func (f *fakeRabbit) publish(_ context.Context, exchange, routingKey string, body []byte) error {
	f.calls = append(f.calls, rabbitCall{exchange: exchange, routingKey: routingKey, body: body})
	return f.err
}
func (f *fakeRabbit) healthy() bool { return f.isHealthy }
func (f *fakeRabbit) close() error  { f.closed++; return nil }

func rabbitTestSub(t *testing.T, cfg rabbitSinkConfig) admindomain.EventSubscription {
	t.Helper()
	b, _ := json.Marshal(cfg)
	return admindomain.EventSubscription{
		SubscriptionID: uuid.New(),
		TenantID:       uuid.New(),
		SinkKind:       "rabbitmq",
		SinkConfig:     b,
	}
}

func rabbitTestDispatcher(fake rabbitPublisher) *Dispatcher {
	pool := NewRabbitMQConnPool(nil)
	pool.newPub = func(string, *tls.Config) (rabbitPublisher, error) { return fake, nil }
	return &Dispatcher{RabbitMQ: pool}
}

func TestDeliverRabbitMQ_PublishesCloudEventEnvelope(t *testing.T) {
	fake := &fakeRabbit{isHealthy: true}
	d := rabbitTestDispatcher(fake)
	sub := rabbitTestSub(t, rabbitSinkConfig{URL: "amqp://h", Exchange: "events", RoutingKey: "paladin.bucket"})
	if _, err := d.deliverRabbitMQ(context.Background(), sub, sinkTestEvent()); err != nil {
		t.Fatalf("deliverRabbitMQ: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("publish calls = %d, want 1", len(fake.calls))
	}
	c := fake.calls[0]
	if c.exchange != "events" || c.routingKey != "paladin.bucket" {
		t.Errorf("routing: exchange=%q routingKey=%q", c.exchange, c.routingKey)
	}
	var env cloudEventEnvelope
	if err := json.Unmarshal(c.body, &env); err != nil {
		t.Fatalf("body is not a CloudEvents envelope: %v", err)
	}
	if env.SpecVersion != "1.0" || env.Type != "paladin.bucket.updated" || env.TenantID != "tenant-123" {
		t.Errorf("envelope mismatch: %+v", env)
	}
}

func TestDeliverRabbitMQ_Errors(t *testing.T) {
	ev := sinkTestEvent()
	d := rabbitTestDispatcher(&fakeRabbit{isHealthy: true})
	if _, err := d.deliverRabbitMQ(context.Background(), rabbitTestSub(t, rabbitSinkConfig{RoutingKey: "k"}), ev); err == nil {
		t.Error("missing url should error")
	}
	if _, err := d.deliverRabbitMQ(context.Background(), rabbitTestSub(t, rabbitSinkConfig{URL: "amqp://h"}), ev); err == nil {
		t.Error("missing routing_key should error")
	}
	dErr := rabbitTestDispatcher(&fakeRabbit{isHealthy: true, err: errors.New("nack")})
	if _, err := dErr.deliverRabbitMQ(context.Background(), rabbitTestSub(t, rabbitSinkConfig{URL: "amqp://h", RoutingKey: "k"}), ev); err == nil {
		t.Error("publish error should propagate")
	}
	if _, err := (&Dispatcher{}).deliverRabbitMQ(context.Background(), rabbitTestSub(t, rabbitSinkConfig{URL: "amqp://h", RoutingKey: "k"}), ev); err == nil {
		t.Error("nil RabbitMQ pool should error")
	}
}

// TestRabbitMQConnPool_RedialsUnhealthy: a cached-but-dead connection must be
// closed + re-dialed on next get, so a broker restart self-heals.
func TestRabbitMQConnPool_RedialsUnhealthy(t *testing.T) {
	dials := 0
	stale := &fakeRabbit{isHealthy: false}
	pool := NewRabbitMQConnPool(nil)
	pool.newPub = func(string, *tls.Config) (rabbitPublisher, error) {
		dials++
		if dials == 1 {
			return stale, nil
		}
		return &fakeRabbit{isHealthy: true}, nil
	}
	if _, err := pool.get("k", "amqp://h", nil); err != nil {
		t.Fatalf("get #1: %v", err)
	}
	if _, err := pool.get("k", "amqp://h", nil); err != nil {
		t.Fatalf("get #2: %v", err)
	}
	if dials != 2 {
		t.Errorf("dials = %d, want 2 (redial on unhealthy cached conn)", dials)
	}
	if stale.closed != 1 {
		t.Errorf("stale conn closed = %d, want 1", stale.closed)
	}
}

func TestBuildRabbitTLS(t *testing.T) {
	if cfg, err := buildRabbitTLS(rabbitSinkConfig{URL: "amqp://h"}); err != nil || cfg != nil {
		t.Errorf("no TLS material → (nil, nil), got (%v, %v)", cfg, err)
	}
	cert, key := genTestKeypair(t)
	cfg, err := buildRabbitTLS(rabbitSinkConfig{TLSClientCert: cert, TLSClientKey: key, TLSCACert: cert})
	if err != nil {
		t.Fatalf("valid material: %v", err)
	}
	if len(cfg.Certificates) != 1 || cfg.RootCAs == nil {
		t.Error("client keypair + RootCAs must both be set")
	}
	if _, err := buildRabbitTLS(rabbitSinkConfig{TLSClientCert: "junk", TLSClientKey: key}); err == nil {
		t.Error("bad keypair must error")
	}
	if _, err := buildRabbitTLS(rabbitSinkConfig{TLSCACert: "junk"}); err == nil {
		t.Error("bad CA must error")
	}
}

func TestRabbitConnKey_TLSDistinguishes(t *testing.T) {
	plain := rabbitSinkConfig{URL: "amqps://h"}
	withCert := plain
	withCert.TLSClientCert = "PEM"
	if rabbitConnKey(plain) == rabbitConnKey(withCert) {
		t.Error("conn key must differ when client-cert material differs")
	}
}
