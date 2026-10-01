package worker

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	kafka "github.com/segmentio/kafka-go"
)

// buildKafkaTransport turns the sink auth config into a *kafka.Transport
// (SASL / TLS). The SASL/TLS handshake itself needs a broker, but the
// transport CONSTRUCTION is fully testable here.

func TestBuildKafkaTransport_Plaintext(t *testing.T) {
	tr, err := buildKafkaTransport(kafkaSinkConfig{Brokers: "b:9092", Topic: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if tr != nil {
		t.Error("no auth → nil transport (plaintext), got non-nil")
	}
}

func TestBuildKafkaTransport_SASL(t *testing.T) {
	cases := []struct{ mech, wantName string }{
		{"plain", "PLAIN"},
		{"scram-sha-256", "SCRAM-SHA-256"},
		{"scram-sha-512", "SCRAM-SHA-512"},
	}
	for _, tc := range cases {
		tr, err := buildKafkaTransport(kafkaSinkConfig{
			SASLMechanism: tc.mech, SASLUsername: "u", SASLPassword: "p",
		})
		if err != nil {
			t.Fatalf("%s: %v", tc.mech, err)
		}
		if tr == nil || tr.SASL == nil {
			t.Fatalf("%s: want a SASL mechanism on the transport", tc.mech)
		}
		if tr.SASL.Name() != tc.wantName {
			t.Errorf("%s: mechanism name = %q, want %q", tc.mech, tr.SASL.Name(), tc.wantName)
		}
		if tr.TLS != nil {
			t.Errorf("%s: TLS should be nil without tls_enabled", tc.mech)
		}
	}
}

func TestBuildKafkaTransport_UnsupportedMechanism(t *testing.T) {
	if _, err := buildKafkaTransport(kafkaSinkConfig{SASLMechanism: "kerberos"}); err == nil {
		t.Error("want an error for an unsupported SASL mechanism")
	}
}

func TestBuildKafkaTransport_TLSAndMTLS(t *testing.T) {
	// tls_enabled alone → server-verified TLS, no client cert (SASL_SSL case).
	tr, err := buildKafkaTransport(kafkaSinkConfig{TLSEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if tr == nil || tr.TLS == nil {
		t.Fatal("tls_enabled → want a TLS config")
	}
	if len(tr.TLS.Certificates) != 0 {
		t.Error("no client cert configured → want zero certs")
	}
	if tr.TLS.MinVersion < tls.VersionTLS12 {
		t.Error("want MinVersion >= TLS 1.2")
	}

	// Valid client cert + key → mTLS (implies TLS even without tls_enabled).
	certPEM, keyPEM := genTestKeypair(t)
	tr, err = buildKafkaTransport(kafkaSinkConfig{
		TLSClientCert: certPEM, TLSClientKey: keyPEM,
	})
	if err != nil {
		t.Fatalf("valid mTLS keypair: %v", err)
	}
	if tr == nil || len(tr.TLS.Certificates) != 1 {
		t.Fatalf("mTLS → want exactly one client cert, got %+v", tr)
	}

	// Malformed PEM → a clear error, not a silent plaintext downgrade.
	if _, err := buildKafkaTransport(kafkaSinkConfig{
		TLSClientCert: "not-a-pem", TLSClientKey: "nope",
	}); err == nil {
		t.Error("want an error for a malformed mTLS keypair")
	}
}

func TestKafkaWriterKey_DistinctPerAuth(t *testing.T) {
	brokers := []string{"b:9092"}
	base := kafkaSinkConfig{Topic: "t"}
	plain256 := kafkaSinkConfig{Topic: "t", SASLMechanism: "scram-sha-256", SASLUsername: "u", SASLPassword: "p"}
	diffPass := kafkaSinkConfig{Topic: "t", SASLMechanism: "scram-sha-256", SASLUsername: "u", SASLPassword: "OTHER"}

	if newKafkaWriterKey(brokers, base) == newKafkaWriterKey(brokers, plain256) {
		t.Error("plaintext and SASL sinks must not share a writer key")
	}
	if newKafkaWriterKey(brokers, plain256) == newKafkaWriterKey(brokers, diffPass) {
		t.Error("same user, different password → distinct writer key")
	}
	// Two equal-but-separate configs → identical key (writers are reused).
	plain256Copy := kafkaSinkConfig{Topic: "t", SASLMechanism: "scram-sha-256", SASLUsername: "u", SASLPassword: "p"}
	if newKafkaWriterKey(brokers, plain256) != newKafkaWriterKey(brokers, plain256Copy) {
		t.Error("identical config → identical key (writers are reused)")
	}
}

// deliverKafka builds a transport from the sink config and threads it into the
// pool's newWriter.
func TestDeliverKafka_ThreadsTransport(t *testing.T) {
	pool := NewKafkaWriterPool(nil)
	var gotTransport *kafka.Transport
	pool.newWriter = func(_ []string, _ string, tr *kafka.Transport) kafkaWriter {
		gotTransport = tr
		return &fakeKafka{}
	}
	d := &Dispatcher{Kafka: pool}
	sub := kafkaTestSub(t, kafkaSinkConfig{
		Brokers: "b:9092", Topic: "t",
		SASLMechanism: "plain", SASLUsername: "u", SASLPassword: "p",
	})
	if _, err := d.deliverKafka(context.Background(), sub, sinkTestEvent()); err != nil {
		t.Fatalf("deliverKafka: %v", err)
	}
	if gotTransport == nil || gotTransport.SASL == nil {
		t.Error("deliverKafka must thread an authenticated transport to newWriter")
	}
}

// genTestKeypair returns a throwaway self-signed cert + key in PEM.
func genTestKeypair(t *testing.T) (certPEM, keyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "paladin-test"},
		NotBefore:    time.Unix(1_700_000_000, 0),
		NotAfter:     time.Unix(1_900_000_000, 0),
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

func TestBuildKafkaTransport_PrivateCA(t *testing.T) {
	ca, _ := genTestKeypair(t)
	tr, err := buildKafkaTransport(kafkaSinkConfig{TLSCACert: ca})
	if err != nil {
		t.Fatalf("valid CA bundle: %v", err)
	}
	if tr == nil || tr.TLS == nil || tr.TLS.RootCAs == nil {
		t.Fatal("tls_ca_cert must imply TLS with a custom RootCAs pool")
	}

	if _, err := buildKafkaTransport(kafkaSinkConfig{TLSCACert: "not pem"}); err == nil {
		t.Error("garbage tls_ca_cert must fail the transport build")
	}
}

func TestKafkaWriterKey_CACertDistinguishes(t *testing.T) {
	base := kafkaSinkConfig{Brokers: "b:9092", Topic: "t", TLSEnabled: true}
	withCA := base
	withCA.TLSCACert = "-----BEGIN CERTIFICATE-----\nAA\n-----END CERTIFICATE-----"
	if newKafkaWriterKey([]string{"b:9092"}, base) == newKafkaWriterKey([]string{"b:9092"}, withCA) {
		t.Error("pool key must differ when tls_ca_cert differs")
	}
}

func TestKafkaWriterKey_BrokersSpellingSharesAWriter(t *testing.T) {
	brokers := []string{"a:9092", "b:9092"}
	spaced := kafkaSinkConfig{Brokers: "a:9092, b:9092", Topic: "t"}
	tight := kafkaSinkConfig{Brokers: "a:9092,b:9092", Topic: "t"}
	if newKafkaWriterKey(brokers, spaced) != newKafkaWriterKey(brokers, tight) {
		t.Error("the same normalised broker list must share a writer")
	}
}

func TestKafkaWriterKey_FormattingOmitsCredentials(t *testing.T) {
	const secret = "s3cret-pass"
	key := newKafkaWriterKey([]string{"b:9092"}, kafkaSinkConfig{
		Topic: "t", SASLMechanism: "plain", SASLUsername: "u", SASLPassword: secret,
		TLSClientKey: secret,
	})
	for _, verb := range []string{"%v", "%s", "%+v"} {
		if got := fmt.Sprintf(verb, key); strings.Contains(got, secret) {
			t.Errorf("%s prints the credential: %q", verb, got)
		}
	}
}
