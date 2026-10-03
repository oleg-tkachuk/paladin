package s3adapter

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/multiparth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/checksum"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

// fakeCreds is a credentials provider whose expiry the test chooses.
type fakeCreds struct {
	expires   time.Time
	canExpire bool
	err       error
}

func (f fakeCreds) Retrieve(context.Context) (aws.Credentials, error) {
	if f.err != nil {
		return aws.Credentials{}, f.err
	}
	return aws.Credentials{
		AccessKeyID: "ASIAEXAMPLE", SecretAccessKey: "secret", SessionToken: "token",
		CanExpire: f.canExpire, Expires: f.expires,
	}, nil
}

// clientWithCreds builds a Client that signs with the given credentials.
func clientWithCreds(t *testing.T, creds aws.CredentialsProvider) *Client {
	t.Helper()
	return newClient(aws.Config{Region: "us-east-1", Credentials: creds}, config.StorageBackend{
		Bucket: "b", Region: "us-east-1", Endpoint: "http://s3.test:8333", ForcePathStyle: true,
	})
}

func urlExpires(t *testing.T, raw string) time.Duration {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	secs, err := strconv.Atoi(u.Query().Get(amzExpiresParam))
	if err != nil {
		t.Fatalf("X-Amz-Expires in %s: %v", raw, err)
	}
	return time.Duration(secs) * time.Second
}

func TestPresignTTL(t *testing.T) {
	const want = time.Hour
	cases := []struct {
		name    string
		creds   fakeCreds
		want    time.Duration
		wantErr bool
	}{
		{"static credentials keep the requested ttl", fakeCreds{}, want, false},
		{"a session outliving the ttl keeps it", fakeCreds{canExpire: true, expires: time.Now().Add(3 * time.Hour)}, want, false},
		{"a session ending sooner shortens it", fakeCreds{canExpire: true, expires: time.Now().Add(10 * time.Minute)}, 10 * time.Minute, false},
		{"a session with under a second left is refused", fakeCreds{canExpire: true, expires: time.Now().Add(500 * time.Millisecond)}, 0, true},
		{"an expired session is refused", fakeCreds{canExpire: true, expires: time.Now().Add(-time.Minute)}, 0, true},
		{"a retrieval failure is reported", fakeCreds{err: errors.New("sts down")}, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := clientWithCreds(t, tc.creds)
			got, err := c.presignTTL(context.Background(), want)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("presignTTL = %v, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// Truncated to whole seconds, so allow the second the test took.
			if d := tc.want - got; d < 0 || d > time.Second {
				t.Fatalf("presignTTL = %v, want %v", got, tc.want)
			}
		})
	}
}

// A URL signed with session credentials used to claim the requested
// lifetime and die with the session: a week-long URL from a one-hour role
// stopped working after an hour while expires_at still said a week.
func TestPresignedURLsAreBoundedBySessionCredentials(t *testing.T) {
	const session = 10 * time.Minute
	c := clientWithCreds(t, fakeCreds{canExpire: true, expires: time.Now().Add(session)})
	ctx := context.Background()
	tenant := testTenant

	sign := map[string]func() (string, time.Time, error){
		"get": func() (string, time.Time, error) {
			u, _, exp, err := c.PresignGet(ctx, objecth.PresignGetArgs{TenantID: tenant, Bucket: "b", Collection: "ok", Key: "k", TTL: 24 * time.Hour})
			return u, exp, err
		},
		"put": func() (string, time.Time, error) {
			u, _, exp, err := c.PresignPut(ctx, objecth.PresignPutArgs{TenantID: tenant, Bucket: "b", Collection: "ok", Key: "k", ContentType: "text/plain",
				SizeBytes: 5, ChecksumAlgo: checksum.SHA256, ChecksumValue: testChecksum, TTL: 24 * time.Hour})
			return u, exp, err
		},
		"part": func() (string, time.Time, error) {
			u, _, exp, err := c.PresignPart(ctx, "b", tenant, "upload-1", "ok", "k",
				multiparth.PartBinding{Number: 1, SizeBytes: 5, ChecksumAlgo: checksum.SHA256, ChecksumValue: testChecksum}, 24*time.Hour)
			return u, exp, err
		},
	}
	for name, fn := range sign {
		t.Run(name, func(t *testing.T) {
			u, exp, err := fn()
			if err != nil {
				t.Fatal(err)
			}
			if got := urlExpires(t, u); got > session {
				t.Fatalf("X-Amz-Expires = %v, outlives the %v session", got, session)
			}
			if time.Until(exp) > session {
				t.Fatalf("reported expiry %v is past the session's end", exp)
			}
		})
	}
}

// expires_at was time.Now()+ttl taken after signing, so it drifted from the
// instant the object store enforces. It is now read from the URL itself.
func TestReportedExpiryIsTheSignedOne(t *testing.T) {
	c := clientWithCreds(t, fakeCreds{})
	u, _, exp, err := c.PresignGet(context.Background(), objecth.PresignGetArgs{
		TenantID: testTenant, Bucket: "b", Collection: "ok", Key: "k", TTL: 15 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	want, err := signedURLExpiry(u)
	if err != nil {
		t.Fatal(err)
	}
	if !exp.Equal(want) {
		t.Fatalf("reported expiry %v, URL says %v", exp, want)
	}
}

func TestSignedURLExpiry(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		want    time.Time
		wantErr bool
	}{
		{"date plus expires", "https://s3/b/k?X-Amz-Date=20261003T120000Z&X-Amz-Expires=900", time.Date(2026, 10, 3, 12, 15, 0, 0, time.UTC), false},
		{"the SigV4 ceiling", "https://s3/b/k?X-Amz-Date=20261003T120000Z&X-Amz-Expires=604800", time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC), false},
		{"no date", "https://s3/b/k?X-Amz-Expires=900", time.Time{}, true},
		{"no expires", "https://s3/b/k?X-Amz-Date=20261003T120000Z", time.Time{}, true},
		{"malformed date", "https://s3/b/k?X-Amz-Date=yesterday&X-Amz-Expires=900", time.Time{}, true},
		{"unparseable url", "://", time.Time{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := signedURLExpiry(tc.url)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("signedURLExpiry = %v, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !got.Equal(tc.want) {
				t.Fatalf("signedURLExpiry = %v, want %v", got, tc.want)
			}
		})
	}
}

// A presigned GET answers with Cache-Control private and a max-age no longer
// than the URL lives, so no shared cache keeps serving the object after the
// URL that authorised it has expired.
func TestPresignGetSignsCacheControl(t *testing.T) {
	c := clientWithCreds(t, fakeCreds{})
	u, _, _, err := c.PresignGet(context.Background(), objecth.PresignGetArgs{
		TenantID: testTenant, Bucket: "b", Collection: "ok", Key: "k", TTL: 15 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Query().Get("response-cache-control"); got != "private, max-age=900" {
		t.Fatalf("response-cache-control = %q, want %q", got, "private, max-age=900")
	}
}
