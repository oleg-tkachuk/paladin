//go:build integration

// Real-broker delivery tests for the event dispatcher's sink implementations.
// The unit suites pin everything up to the wire via seams (fake writer /
// publisher / sender); these spin the actual brokers in testcontainers and
// prove the handshake + wire format land: a delivery through DeliverOne is
// consumed back off the broker as a valid CloudEvents 1.0 envelope.
//
// Coverage deliberately matches the BACKLOG asks:
//   - Kafka: SASL/SCRAM-SHA-256 against a real redpanda (the handshake the
//     unit seam can't reach). TLS/mTLS handshake still needs a certs-mounted
//     broker — recorded as the remaining tail.
//   - RabbitMQ: publisher-confirmed publish → consume round-trip.
//   - SQS: SendMessage → ReceiveMessage round-trip against elasticmq.
//
// Requires Docker; run via `task backend:test:integration` (tags=integration).
package integration

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	kafka "github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/scram"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/redpanda"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// brokerTestSub builds the subscription row DeliverOne dispatches on.
func brokerTestSub(t *testing.T, sinkKind string, cfg map[string]any) admindomain.EventSubscription {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal sink config: %v", err)
	}
	return admindomain.EventSubscription{
		SubscriptionID: uuid.Must(uuid.NewV7()),
		TenantID:       uuid.Must(uuid.NewV7()),
		SinkKind:       sinkKind,
		SinkConfig:     raw,
	}
}

// assertEnvelope decodes a consumed body and pins the CloudEvents contract.
func assertEnvelope(t *testing.T, body []byte, wantType string) {
	t.Helper()
	var env struct {
		SpecVersion string `json:"specversion"`
		Type        string `json:"type"`
		Source      string `json:"source"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("consumed body is not JSON: %v (%q)", err, body)
	}
	if env.SpecVersion != "1.0" || env.Type != wantType || env.Source != "paladin" {
		t.Errorf("envelope = %+v, want specversion=1.0 type=%s source=paladin", env, wantType)
	}
}

// ─── Kafka (redpanda, SASL/SCRAM-SHA-256) ────────────────────────────────────

func TestKafkaSinkDelivery_SCRAM(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	const user, pass = "paladin-writer", "paladin-secret-pw"

	rp, err := redpanda.Run(ctx, "docker.redpanda.com/redpandadata/redpanda:v24.3.6",
		redpanda.WithEnableSASL(),
		redpanda.WithEnableKafkaAuthorization(),
		redpanda.WithNewServiceAccount(user, pass),
		redpanda.WithSuperusers(user),
	)
	if err != nil {
		t.Fatalf("start redpanda: %v", err)
	}
	defer func() { _ = testcontainers.TerminateContainer(rp) }()
	broker, err := rp.KafkaSeedBroker(ctx)
	if err != nil {
		t.Fatalf("seed broker: %v", err)
	}

	const topic = "paladin-events-it"
	// Create the topic as the SCRAM user — also proves the credentials are
	// live before the delivery path runs.
	mech, err := scram.Mechanism(scram.SHA256, user, pass)
	if err != nil {
		t.Fatalf("scram mechanism: %v", err)
	}
	transport := &kafka.Transport{SASL: mech}
	kc := &kafka.Client{Addr: kafka.TCP(broker), Transport: transport}
	if _, err := kc.CreateTopics(ctx, &kafka.CreateTopicsRequest{
		Topics: []kafka.TopicConfig{{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}},
	}); err != nil {
		t.Fatalf("create topic: %v", err)
	}

	d := &worker.Dispatcher{Kafka: worker.NewKafkaWriterPool(nil), MaxAttempts: 1}
	defer d.Kafka.Close()
	sub := brokerTestSub(t, "kafka", map[string]any{
		"brokers":        broker,
		"topic":          topic,
		"sasl_mechanism": "scram-sha-256",
		"sasl_username":  user,
		"sasl_password":  pass,
	})
	if err := d.DeliverOne(ctx, sub, "paladin.bucket.updated"); err != nil {
		t.Fatalf("DeliverOne(kafka): %v", err)
	}

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: []string{broker},
		Topic:   topic,
		Dialer: &kafka.Dialer{
			Timeout:       10 * time.Second,
			SASLMechanism: mech,
		},
		StartOffset: kafka.FirstOffset,
		MaxWait:     time.Second,
	})
	defer func() { _ = reader.Close() }()
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	msg, err := reader.ReadMessage(rctx)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if string(msg.Key) != sub.TenantID.String() {
		t.Errorf("message key = %q, want tenant id %s (per-tenant partitioning)", msg.Key, sub.TenantID)
	}
	assertEnvelope(t, msg.Value, "paladin.bucket.updated")
}

// ─── RabbitMQ ────────────────────────────────────────────────────────────────

func TestRabbitMQSinkDelivery(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	const user, pass = "paladin", "paladin-secret-pw"

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "rabbitmq:4.0-alpine",
			ExposedPorts: []string{"5672/tcp"},
			Env: map[string]string{
				"RABBITMQ_DEFAULT_USER": user,
				"RABBITMQ_DEFAULT_PASS": pass,
			},
			WaitingFor: wait.ForLog("Server startup complete").WithStartupTimeout(90 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start rabbitmq: %v", err)
	}
	defer func() { _ = testcontainers.TerminateContainer(ctr) }()
	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := ctr.MappedPort(ctx, "5672/tcp")
	if err != nil {
		t.Fatal(err)
	}
	url := fmt.Sprintf("amqp://%s:%s@%s:%s/", user, pass, host, port.Port())

	// Declare the target queue out-of-band (the sink publishes to the default
	// exchange with routing_key == queue name — the documented contract).
	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatalf("amqp dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	const queue = "paladin-events-it"
	if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
		t.Fatalf("declare queue: %v", err)
	}

	pool := worker.NewRabbitMQConnPool(nil)
	defer pool.Close()
	d := &worker.Dispatcher{RabbitMQ: pool, MaxAttempts: 1}
	sub := brokerTestSub(t, "rabbitmq", map[string]any{
		"url":         url,
		"exchange":    "", // default exchange
		"routing_key": queue,
	})
	if err := d.DeliverOne(ctx, sub, "paladin.bucket.updated"); err != nil {
		t.Fatalf("DeliverOne(rabbitmq): %v", err)
	}

	msg, ok, err := ch.Get(queue, true)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if !ok {
		t.Fatal("queue empty — publisher-confirmed publish did not land")
	}
	assertEnvelope(t, msg.Body, "paladin.bucket.updated")
}

// ─── SQS (elasticmq) ─────────────────────────────────────────────────────────

func TestSQSSinkDelivery(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "softwaremill/elasticmq-native:1.6.11",
			ExposedPorts: []string{"9324/tcp"},
			WaitingFor:   wait.ForListeningPort("9324/tcp").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start elasticmq: %v", err)
	}
	defer func() { _ = testcontainers.TerminateContainer(ctr) }()
	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := ctr.MappedPort(ctx, "9324/tcp")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := fmt.Sprintf("http://%s:%s", host, port.Port())

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("elasticmq"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("x", "x", "")),
	)
	if err != nil {
		t.Fatal(err)
	}
	client := awssqs.NewFromConfig(awsCfg, func(o *awssqs.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
	created, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("paladin-events-it")})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}

	// The dispatcher's pool builds its own client from ambient config —
	// point it at elasticmq via the standard env the AWS SDK honours.
	t.Setenv("AWS_ACCESS_KEY_ID", "x")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "x")
	t.Setenv("AWS_REGION", "elasticmq")
	t.Setenv("AWS_ENDPOINT_URL", endpoint)

	d := &worker.Dispatcher{SQS: worker.NewSQSClientPool(nil), MaxAttempts: 1}
	sub := brokerTestSub(t, "sqs", map[string]any{
		"queue_url": *created.QueueUrl,
		"region":    "elasticmq",
	})
	if err := d.DeliverOne(ctx, sub, "paladin.bucket.updated"); err != nil {
		t.Fatalf("DeliverOne(sqs): %v", err)
	}

	got, err := client.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl:            created.QueueUrl,
		MaxNumberOfMessages: 1,
		WaitTimeSeconds:     10,
	})
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(got.Messages))
	}
	assertEnvelope(t, []byte(*got.Messages[0].Body), "paladin.bucket.updated")
}

// genServerCert issues a self-signed server certificate with localhost SANs —
// the shape a certs-mounted broker presents; the cert doubles as the sink's
// tls_ca_cert (self-signed == its own CA).
func genServerCert(t *testing.T) (certPEM, keyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "paladin-it-broker"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	return certPEM, keyPEM
}

// TestKafkaSinkDelivery_SASL_SSL exercises the full SASL_SSL posture against
// a certs-mounted redpanda: SCRAM over TLS, the broker's self-signed server
// cert verified via the sink's tls_ca_cert (private-CA path — system roots
// can't validate it). Closes the "TLS handshake against a certs-mounted
// broker" tail.
func TestKafkaSinkDelivery_SASL_SSL(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	const user, pass = "paladin-writer", "paladin-secret-pw"
	certPEM, keyPEM := genServerCert(t)

	rp, err := redpanda.Run(ctx, "docker.redpanda.com/redpandadata/redpanda:v24.3.6",
		redpanda.WithTLS([]byte(certPEM), []byte(keyPEM)),
		redpanda.WithEnableSASL(),
		redpanda.WithNewServiceAccount(user, pass),
		redpanda.WithSuperusers(user),
	)
	if err != nil {
		t.Fatalf("start redpanda(tls): %v", err)
	}
	defer func() { _ = testcontainers.TerminateContainer(rp) }()
	broker, err := rp.KafkaSeedBroker(ctx)
	if err != nil {
		t.Fatalf("seed broker: %v", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(certPEM)) {
		t.Fatal("append CA")
	}
	tlsCfg := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	mech, err := scram.Mechanism(scram.SHA256, user, pass)
	if err != nil {
		t.Fatal(err)
	}

	const topic = "paladin-events-ssl-it"
	kc := &kafka.Client{Addr: kafka.TCP(broker), Transport: &kafka.Transport{SASL: mech, TLS: tlsCfg}}
	if _, err := kc.CreateTopics(ctx, &kafka.CreateTopicsRequest{
		Topics: []kafka.TopicConfig{{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}},
	}); err != nil {
		t.Fatalf("create topic over SASL_SSL: %v", err)
	}

	d := &worker.Dispatcher{Kafka: worker.NewKafkaWriterPool(nil), MaxAttempts: 1}
	defer d.Kafka.Close()
	sub := brokerTestSub(t, "kafka", map[string]any{
		"brokers":        broker,
		"topic":          topic,
		"sasl_mechanism": "scram-sha-256",
		"sasl_username":  user,
		"sasl_password":  pass,
		"tls_enabled":    true,
		"tls_ca_cert":    certPEM,
	})
	if err := d.DeliverOne(ctx, sub, "paladin.bucket.updated"); err != nil {
		t.Fatalf("DeliverOne(kafka SASL_SSL): %v", err)
	}

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: []string{broker},
		Topic:   topic,
		Dialer: &kafka.Dialer{
			Timeout:       10 * time.Second,
			TLS:           tlsCfg,
			SASLMechanism: mech,
		},
		StartOffset: kafka.FirstOffset,
		MaxWait:     time.Second,
	})
	defer func() { _ = reader.Close() }()
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	msg, err := reader.ReadMessage(rctx)
	if err != nil {
		t.Fatalf("consume over SASL_SSL: %v", err)
	}
	assertEnvelope(t, msg.Value, "paladin.bucket.updated")
}

// genCASignedPair issues a CA plus a CA-signed leaf (server or client).
func genCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "paladin-it-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	return ca, key, pemStr
}

func genLeaf(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, cn string, server bool) (certPEM, keyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	if server {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		tmpl.DNSNames = []string{"localhost"}
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	} else {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	return certPEM, keyPEM
}

// TestRabbitMQSinkDelivery_AMQPSClientCert runs the sink against a RabbitMQ
// whose TLS listener REQUIRES a client certificate (verify_peer +
// fail_if_no_peer_cert): the delivery only succeeds because buildRabbitTLS
// presents the sink's tls_client_cert; the same config minus the client
// keypair must fail the handshake. Closes the "AMQPS with TLS client certs"
// tail.
func TestRabbitMQSinkDelivery_AMQPSClientCert(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	const user, pass = "paladin", "paladin-secret-pw"

	ca, caKey, caPEM := genCA(t)
	srvCert, srvKey := genLeaf(t, ca, caKey, "rabbit-server", true)
	cliCert, cliKey := genLeaf(t, ca, caKey, "paladin-dispatcher", false)

	sslConf := `listeners.ssl.default = 5671
ssl_options.cacertfile = /certs/ca.pem
ssl_options.certfile = /certs/server.pem
ssl_options.keyfile = /certs/server-key.pem
ssl_options.verify = verify_peer
ssl_options.fail_if_no_peer_cert = true
`
	files := []testcontainers.ContainerFile{
		{Reader: strings.NewReader(caPEM), ContainerFilePath: "/certs/ca.pem", FileMode: 0o644},
		{Reader: strings.NewReader(srvCert), ContainerFilePath: "/certs/server.pem", FileMode: 0o644},
		{Reader: strings.NewReader(srvKey), ContainerFilePath: "/certs/server-key.pem", FileMode: 0o644},
		{Reader: strings.NewReader(sslConf), ContainerFilePath: "/etc/rabbitmq/conf.d/10-ssl.conf", FileMode: 0o644},
	}

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "rabbitmq:4.0-alpine",
			ExposedPorts: []string{"5671/tcp", "5672/tcp"},
			Env: map[string]string{
				"RABBITMQ_DEFAULT_USER": user,
				"RABBITMQ_DEFAULT_PASS": pass,
			},
			Files:      files,
			WaitingFor: wait.ForLog("Server startup complete").WithStartupTimeout(90 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start rabbitmq(tls): %v", err)
	}
	defer func() { _ = testcontainers.TerminateContainer(ctr) }()
	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tlsPort, err := ctr.MappedPort(ctx, "5671/tcp")
	if err != nil {
		t.Fatal(err)
	}
	amqpsURL := fmt.Sprintf("amqps://%s:%s@%s:%s/", user, pass, host, tlsPort.Port())

	// Declare the destination queue over the SAME mutually-authenticated
	// listener (also proves the cert chain end-to-end before the sink runs).
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM([]byte(caPEM))
	cliPair, err := tls.X509KeyPair([]byte(cliCert), []byte(cliKey))
	if err != nil {
		t.Fatal(err)
	}
	adminConn, err := amqp.DialTLS(amqpsURL, &tls.Config{RootCAs: caPool, Certificates: []tls.Certificate{cliPair}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatalf("amqps admin dial: %v", err)
	}
	defer func() { _ = adminConn.Close() }()
	ch, err := adminConn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	const queue = "paladin-events-amqps-it"
	if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
		t.Fatalf("declare queue: %v", err)
	}

	// NEGATIVE: no client cert → the broker must refuse the handshake.
	if noCert, err := amqp.DialTLS(amqpsURL, &tls.Config{RootCAs: caPool, MinVersion: tls.VersionTLS12}); err == nil {
		_ = noCert.Close()
		t.Fatal("dial WITHOUT a client cert must fail against fail_if_no_peer_cert")
	}

	pool := worker.NewRabbitMQConnPool(nil)
	defer pool.Close()
	d := &worker.Dispatcher{RabbitMQ: pool, MaxAttempts: 1}
	sub := brokerTestSub(t, "rabbitmq", map[string]any{
		"url":             amqpsURL,
		"exchange":        "",
		"routing_key":     queue,
		"tls_client_cert": cliCert,
		"tls_client_key":  cliKey,
		"tls_ca_cert":     caPEM,
	})
	if err := d.DeliverOne(ctx, sub, "paladin.bucket.updated"); err != nil {
		t.Fatalf("DeliverOne(rabbitmq AMQPS client-cert): %v", err)
	}

	msg, ok, err := ch.Get(queue, true)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if !ok {
		t.Fatal("queue empty — confirmed publish did not land")
	}
	assertEnvelope(t, msg.Body, "paladin.bucket.updated")
}
