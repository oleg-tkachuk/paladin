package s3adapter

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/features"
)

// policyMode is how probeStore treats unsigned reads.
type policyMode int

const (
	// policyEnforced serves an unsigned GET under the policy's prefix only.
	policyEnforced policyMode = iota
	// policyIgnored accepts a policy and never evaluates it.
	policyIgnored
	// policyWholeBucket serves the whole bucket once any policy is set.
	policyWholeBucket
	// policyAnonymousAll serves every unsigned GET, policy or not.
	policyAnonymousAll
	// policyNotImplemented answers PutBucketPolicy with 501.
	policyNotImplemented
)

// probeStore is an in-memory S3 with each behaviour the probe checks able to
// go wrong on its own.
type probeStore struct {
	t   *testing.T
	srv *httptest.Server

	ignoreIfNoneMatch  bool
	ifNoneMatchFails   bool // 500 on a conditional PUT
	ignoreChecksum     bool
	noMultipart        bool
	noCopy             bool
	noPost             bool
	refuseCreateBucket bool
	refuseDeleteBucket bool
	policy             policyMode

	mu       sync.Mutex
	buckets  map[string]map[string][]byte
	prefixes map[string]string // bucket → prefix its policy opens
	parts    map[string][]byte // uploadId → part bytes
}

const (
	probeTestConfigured = "configured"
	probeTestUploadID   = "probe-upload"
	probeTestPartETag   = `"part"`
)

func newProbeStore(t *testing.T, mut func(*probeStore)) *probeStore {
	t.Helper()
	s := &probeStore{
		t:        t,
		buckets:  map[string]map[string][]byte{probeTestConfigured: {}},
		prefixes: map[string]string{},
		parts:    map[string][]byte{},
	}
	if mut != nil {
		mut(s)
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *probeStore) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Header.Get("Content-Encoding") == "aws-chunked" {
		s.t.Errorf("%s %s: an aws-chunked body this fake does not decode", r.Method, r.URL.Path)
	}
	body, _ := io.ReadAll(r.Body)
	bucket, key := splitPathStyle(r.URL.Path)
	q := r.URL.Query()
	signed := r.Header.Get("Authorization") != ""
	objects, exists := s.buckets[bucket]

	switch {
	case key == "" && r.Method == http.MethodPut && q.Has("policy"):
		if s.policy == policyNotImplemented {
			writeS3Error(w, http.StatusNotImplemented, "NotImplemented", "bucket policies")
			return
		}
		var doc struct {
			Statement []struct{ Resource []string }
		}
		if err := json.Unmarshal(body, &doc); err != nil || len(doc.Statement) != 1 || len(doc.Statement[0].Resource) != 1 {
			writeS3Error(w, http.StatusBadRequest, "MalformedPolicy", string(body))
			return
		}
		arn := "arn:aws:s3:::" + bucket + "/"
		s.prefixes[bucket] = strings.TrimSuffix(strings.TrimPrefix(doc.Statement[0].Resource[0], arn), "*")
		w.WriteHeader(http.StatusNoContent)
	case key == "" && r.Method == http.MethodDelete && q.Has("policy"):
		delete(s.prefixes, bucket)
		w.WriteHeader(http.StatusNoContent)
	case key == "" && r.Method == http.MethodPut:
		if s.refuseCreateBucket {
			writeS3Error(w, http.StatusForbidden, "AccessDenied", "create bucket")
			return
		}
		s.buckets[bucket] = map[string][]byte{}
		w.WriteHeader(http.StatusOK)
	case key == "" && r.Method == http.MethodHead:
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	case key == "" && r.Method == http.MethodDelete:
		if s.refuseDeleteBucket {
			writeS3Error(w, http.StatusForbidden, "AccessDenied", "delete bucket")
			return
		}
		delete(s.buckets, bucket)
		w.WriteHeader(http.StatusNoContent)
	case key == "" && r.Method == http.MethodPost:
		s.formUpload(w, r, body, objects)
	case !exists:
		writeS3Error(w, http.StatusNotFound, "NoSuchBucket", bucket)
	case r.Method == http.MethodPost && q.Has("uploads"):
		if s.noMultipart {
			writeS3Error(w, http.StatusNotImplemented, "NotImplemented", "multipart")
			return
		}
		writeXML(w, http.StatusOK, `<InitiateMultipartUploadResult><UploadId>`+probeTestUploadID+`</UploadId></InitiateMultipartUploadResult>`)
	case r.Method == http.MethodPut && q.Get("uploadId") != "":
		s.parts[q.Get("uploadId")] = body
		w.Header().Set("ETag", probeTestPartETag)
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodPost && q.Get("uploadId") != "":
		objects[key] = s.parts[q.Get("uploadId")]
		writeXML(w, http.StatusOK, `<CompleteMultipartUploadResult><ETag>&quot;done&quot;</ETag></CompleteMultipartUploadResult>`)
	case r.Method == http.MethodDelete && q.Get("uploadId") != "":
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPut && r.Header.Get("x-amz-copy-source") != "":
		if s.noCopy {
			writeS3Error(w, http.StatusNotImplemented, "NotImplemented", "copy")
			return
		}
		_, srcKey := splitPathStyle("/" + r.Header.Get("x-amz-copy-source"))
		objects[key] = objects[srcKey]
		writeXML(w, http.StatusOK, `<CopyObjectResult><ETag>&quot;copy&quot;</ETag></CopyObjectResult>`)
	case r.Method == http.MethodPut:
		s.putObject(w, r, body, objects, key)
	case r.Method == http.MethodHead:
		b, ok := objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(b)))
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodGet:
		if !signed && !s.servesUnsigned(bucket, key) {
			writeS3Error(w, http.StatusForbidden, "AccessDenied", "unsigned")
			return
		}
		b, ok := objects[key]
		if !ok {
			writeS3Error(w, http.StatusNotFound, "NoSuchKey", key)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	case r.Method == http.MethodDelete:
		delete(objects, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeS3Error(w, http.StatusBadRequest, "BadRequest", "unhandled in probe store")
	}
}

func (s *probeStore) putObject(w http.ResponseWriter, r *http.Request, body []byte, objects map[string][]byte, key string) {
	if r.Header.Get("If-None-Match") == ifNoneMatchAny {
		if s.ifNoneMatchFails {
			writeS3Error(w, http.StatusInternalServerError, "InternalError", "conditional")
			return
		}
		if _, taken := objects[key]; taken && !s.ignoreIfNoneMatch {
			writeS3Error(w, http.StatusPreconditionFailed, "PreconditionFailed", key)
			return
		}
	}
	if want := r.Header.Get("x-amz-checksum-sha256"); want != "" && !s.ignoreChecksum {
		sum := sha256.Sum256(body)
		if base64.StdEncoding.EncodeToString(sum[:]) != want {
			writeS3Error(w, http.StatusBadRequest, "BadDigest", key)
			return
		}
	}
	objects[key] = body
	w.WriteHeader(http.StatusOK)
}

func (s *probeStore) formUpload(w http.ResponseWriter, r *http.Request, body []byte, objects map[string][]byte) {
	if s.noPost || objects == nil {
		writeS3Error(w, http.StatusForbidden, "AccessDenied", "form upload")
		return
	}
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeS3Error(w, http.StatusBadRequest, "MalformedPOSTRequest", err.Error())
		return
	}
	f, _, err := r.FormFile(probeFormFile)
	if err != nil {
		writeS3Error(w, http.StatusBadRequest, "MalformedPOSTRequest", err.Error())
		return
	}
	b, _ := io.ReadAll(f)
	objects[r.FormValue("key")] = b
	w.WriteHeader(http.StatusNoContent)
}

func (s *probeStore) servesUnsigned(bucket, key string) bool {
	prefix, set := s.prefixes[bucket]
	switch s.policy {
	case policyAnonymousAll:
		return true
	case policyWholeBucket:
		return set
	case policyEnforced:
		return set && strings.HasPrefix(key, prefix)
	case policyIgnored, policyNotImplemented:
	}
	return false
}

// leftovers lists every bucket and key the store still holds beyond an empty
// configured bucket.
func (s *probeStore) leftovers() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for b, objects := range s.buckets {
		if b != probeTestConfigured {
			out = append(out, "bucket "+b)
		}
		for k := range objects {
			out = append(out, b+"/"+k)
		}
	}
	sort.Strings(out)
	return out
}

func probeWith(t *testing.T, mut func(*probeStore), backend ...func(*config.StorageBackend)) (*probeStore, map[features.Feature]features.Result) {
	t.Helper()
	store := newProbeStore(t, mut)
	c := newTestClient(t, store.srv.URL, append([]func(*config.StorageBackend){func(b *config.StorageBackend) {
		b.Bucket = probeTestConfigured
	}}, backend...)...)
	results := c.ProbeFeatures(context.Background())
	if len(results) != len(features.Catalog) {
		t.Fatalf("%d results, want one per catalog feature", len(results))
	}
	got := map[features.Feature]features.Result{}
	for i, r := range results {
		if r.Feature != features.Catalog[i].Feature {
			t.Errorf("result %d is %s, want catalog order (%s)", i, r.Feature, features.Catalog[i].Feature)
		}
		if r.CheckedAt.IsZero() {
			t.Errorf("%s carries no checked_at", r.Feature)
		}
		got[r.Feature] = r
	}
	return store, got
}

func TestProbeFindsEveryFeatureOnAStoreThatHasThem(t *testing.T) {
	store, got := probeWith(t, nil)
	for f, r := range got {
		if r.Support != features.Supported {
			t.Errorf("%s = %s (%s), want supported", f, r.Support, r.Message)
		}
	}
	if left := store.leftovers(); len(left) != 0 {
		t.Errorf("the probe left %v behind", left)
	}
}

// Each case breaks one behaviour and expects exactly that feature to say so;
// the rest stay supported.
func TestProbeNamesTheFeatureAStoreLacks(t *testing.T) {
	for name, tc := range map[string]struct {
		break_  func(*probeStore)
		feature features.Feature
		support features.Support
		message string
	}{
		"If-None-Match ignored": {func(s *probeStore) { s.ignoreIfNoneMatch = true },
			features.ConditionalPut, features.Unsupported, "replaced an existing object"},
		"If-None-Match a server error": {func(s *probeStore) { s.ifNoneMatchFails = true },
			features.ConditionalPut, features.Unknown, "InternalError"},
		"checksum ignored": {func(s *probeStore) { s.ignoreChecksum = true },
			features.ChecksumSHA256, features.Unsupported, "did not match"},
		"no multipart": {func(s *probeStore) { s.noMultipart = true },
			features.MultipartUpload, features.Unsupported, "NotImplemented"},
		"no copy": {func(s *probeStore) { s.noCopy = true },
			features.ServerSideCopy, features.Unsupported, "NotImplemented"},
		"no form upload": {func(s *probeStore) { s.noPost = true },
			features.PresignedPost, features.Unsupported, "answered 403"},
		"a bucket it cannot delete": {func(s *probeStore) { s.refuseDeleteBucket = true },
			features.BucketCreate, features.Unsupported, "could not delete it"},
		"a policy accepted and ignored": {func(s *probeStore) { s.policy = policyIgnored },
			features.AnonymousReadPolicy, features.Unsupported, "accepted the policy and refused"},
		"a policy that opens the whole bucket": {func(s *probeStore) { s.policy = policyWholeBucket },
			features.AnonymousReadPolicy, features.Unsupported, "opened the whole bucket"},
		"unsigned reads served without a policy": {func(s *probeStore) { s.policy = policyAnonymousAll },
			features.AnonymousReadPolicy, features.Unsupported, "no policy set"},
		"no bucket policies": {func(s *probeStore) { s.policy = policyNotImplemented },
			features.AnonymousReadPolicy, features.Unsupported, "NotImplemented"},
	} {
		t.Run(name, func(t *testing.T) {
			_, got := probeWith(t, tc.break_)
			for f, r := range got {
				switch {
				case f == tc.feature:
					if r.Support != tc.support || !strings.Contains(r.Message, tc.message) {
						t.Errorf("%s = %s (%s), want %s mentioning %q", f, r.Support, r.Message, tc.support, tc.message)
					}
				case r.Support != features.Supported:
					t.Errorf("%s = %s (%s), want supported: only %s was broken", f, r.Support, r.Message, tc.feature)
				}
			}
		})
	}
}

// Without a scratch bucket the object probes run under the reserved prefix
// of the configured bucket and clean up after themselves; the policy probe,
// which must not touch a live bucket's policy, cannot run.
func TestProbeWithoutAScratchBucket(t *testing.T) {
	store, got := probeWith(t, func(s *probeStore) { s.refuseCreateBucket = true })
	want := map[features.Feature]features.Support{
		features.BucketCreate:        features.Unsupported,
		features.AnonymousReadPolicy: features.Unknown,
	}
	for f, r := range got {
		w, special := want[f]
		if !special {
			w = features.Supported
		}
		if r.Support != w {
			t.Errorf("%s = %s (%s), want %s", f, r.Support, r.Message, w)
		}
	}
	if left := store.leftovers(); len(left) != 0 {
		t.Errorf("the probe left %v behind", left)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, set := store.prefixes[probeTestConfigured]; set {
		t.Error("the probe set a policy on the configured bucket")
	}
}

func TestProbeWithNoBucketAtAll(t *testing.T) {
	_, got := probeWith(t, func(s *probeStore) { s.refuseCreateBucket = true },
		func(b *config.StorageBackend) { b.Bucket = "" })
	for f, r := range got {
		if f == features.BucketCreate {
			continue
		}
		if r.Support != features.Unknown {
			t.Errorf("%s = %s (%s), want unknown with nowhere to probe", f, r.Support, r.Message)
		}
	}
}

func TestProbeOfAnUnreachableStoreIsUnknown(t *testing.T) {
	store := newProbeStore(t, nil)
	c := newTestClient(t, store.srv.URL)
	store.srv.Close()
	for _, r := range c.ProbeFeatures(context.Background()) {
		if r.Support != features.Unknown {
			t.Errorf("%s = %s (%s), want unknown for a store that never answered", r.Feature, r.Support, r.Message)
		}
	}
}

func TestRenderAnonymousReadPolicy(t *testing.T) {
	doc, err := renderAnonymousReadPolicy("public-photos", "t1/c/")
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Statement []struct {
			Effect, Principal string
			Action, Resource  []string
		}
	}
	if err := json.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, doc)
	}
	if len(parsed.Statement) != 1 {
		t.Fatalf("%d statements, want 1", len(parsed.Statement))
	}
	st := parsed.Statement[0]
	if st.Effect != "Allow" || st.Principal != "*" ||
		len(st.Action) != 1 || st.Action[0] != "s3:GetObject" ||
		len(st.Resource) != 1 || st.Resource[0] != "arn:aws:s3:::public-photos/t1/c/*" {
		t.Errorf("statement = %+v, want anonymous GetObject on public-photos/t1/c/*", st)
	}
	whole, err := renderAnonymousReadPolicy("public-photos", "")
	if err != nil || !strings.Contains(whole, `"arn:aws:s3:::public-photos/*"`) {
		t.Errorf("an empty prefix = %q, %v; want the whole bucket", whole, err)
	}
	for _, bad := range []struct{ bucket, prefix string }{
		{"Upper", ""}, {"a", ""}, {`b"x`, ""},
		{"ok-bucket", "*"}, {"ok-bucket", `x"/`}, {"ok-bucket", "no-slash"},
	} {
		if _, err := renderAnonymousReadPolicy(bad.bucket, bad.prefix); err == nil {
			t.Errorf("rendered a policy for bucket %q prefix %q", bad.bucket, bad.prefix)
		}
	}
}
