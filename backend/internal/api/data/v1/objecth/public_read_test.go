package objecth

import (
	"context"
	"regexp"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/checksum"
	"github.com/oleg-tkachuk/paladin/backend/internal/publicread"
	"github.com/oleg-tkachuk/paladin/backend/internal/uploadpolicy"
)

// publicKeyShape is a key the server names a public object with.
var publicKeyShape = regexp.MustCompile(`^[a-z2-7]{26}$`)

const testPublicBase = "https://cdn.example"

// publicURLStorage is recordingUploadStorage that answers PublicURL as the
// adapter would under a CDN base.
type publicURLStorage struct {
	recordingUploadStorage
}

func (*publicURLStorage) PublicURL(_ context.Context, _, _, base string, tenantID uuid.UUID, collection, key string) (string, error) {
	return base + "/" + tenantID.String() + "/" + collection + "/" + key, nil
}

func publicMeta() BucketMeta {
	return BucketMeta{
		BackendID: "backend-7", BucketName: "public-7",
		Constraints:   uploadpolicy.BucketConstraints{AllowedContentTypes: []string{"image/jpeg", "image/svg+xml"}},
		PublicRead:    true,
		CacheControl:  publicread.DefaultCacheControl,
		PublicBaseURL: testPublicBase,
	}
}

func publicUploadHandler() (*Handler, *uploadRepoRecording, *publicURLStorage, context.Context, uuid.UUID) {
	tenantID := uuid.New()
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})
	repo := &uploadRepoRecording{uploadRepo: uploadRepo{fakeObjectRepo: fakeObjectRepo{meta: publicMeta()}}}
	storage := &publicURLStorage{}
	return &Handler{repo: repo, storage: storage, policy: &recordingAuthorizer{}, presign: testPresignConfig()}, repo, storage, ctx, tenantID
}

func photo(post bool) UploadObjectInput {
	return UploadObjectInput{
		Collection: "photos", ContentType: "image/jpeg", SizeBytes: 5, TransportPOST: post,
		ChecksumAlgo: checksum.SHA256, ChecksumValue: testChecksumValue,
	}
}

// The server names the object, records its public URL, and binds the
// collection's Cache-Control into whichever upload is signed (ADR-0027).
func TestUploadIntoAPublicCollection(t *testing.T) {
	for name, post := range map[string]bool{"PUT": false, "POST": true} {
		t.Run(name, func(t *testing.T) {
			h, repo, storage, ctx, tenantID := publicUploadHandler()
			var created CreateObjectArgs
			repo.onCreate = func(a CreateObjectArgs) { created = a }
			if _, err := h.UploadObject(ctx, photo(post)); err != nil {
				t.Fatalf("UploadObject: %v", err)
			}
			if !publicKeyShape.MatchString(created.Key) {
				t.Errorf("key %q is not one the server names a public object with", created.Key)
			}
			if want := testPublicBase + "/" + tenantID.String() + "/photos/" + created.Key; created.PublicURL != want {
				t.Errorf("public url = %q, want %q", created.PublicURL, want)
			}
			var cc, signedKey string
			if post {
				cc, signedKey = storage.post.CacheControl, storage.post.Key
			} else {
				cc, signedKey = storage.put.CacheControl, storage.put.Key
			}
			if cc != publicread.DefaultCacheControl || signedKey != created.Key {
				t.Errorf("signed key %q with cache control %q, want %q with %q", signedKey, cc, created.Key, publicread.DefaultCacheControl)
			}
		})
	}
}

func TestUploadIntoAPublicCollectionRefusals(t *testing.T) {
	withKey := photo(false)
	withKey.Key = "chosen-by-the-client"
	svg := photo(false)
	svg.ContentType = "image/svg+xml"
	for name, in := range map[string]UploadObjectInput{
		"a key the client chose": withKey,
		// On the bucket's allowlist, yet still active content.
		"an active content type": svg,
	} {
		t.Run(name, func(t *testing.T) {
			h, repo, storage, ctx, _ := publicUploadHandler()
			_, err := h.UploadObject(ctx, in)
			if connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("code = %v (%v), want FailedPrecondition", connect.CodeOf(err), err)
			}
			if repo.created || storage.put != nil {
				t.Error("a refused upload created a row or signed a URL")
			}
		})
	}
}

// Outside a public collection nothing changes: the client's key stands, and
// no public URL or Cache-Control appears.
func TestUploadIntoAPrivateCollectionIsUnchanged(t *testing.T) {
	h, repo, storage, ctx := limitsHandler(uploadpolicy.BucketConstraints{}, testUploadLimits)
	var created CreateObjectArgs
	repo.onCreate = func(a CreateObjectArgs) { created = a }
	in := photo(false)
	in.Key = "mine"
	if _, err := h.UploadObject(ctx, in); err != nil {
		t.Fatal(err)
	}
	if created.Key != "mine" || created.PublicURL != "" || storage.put.CacheControl != "" {
		t.Errorf("created %+v signed %+v, want the private upload untouched", created, storage.put)
	}
}

func TestSoftDeleteOfAPublicObjectIsRefused(t *testing.T) {
	tenantID := uuid.New()
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})
	obj := Object{ObjectID: uuid.New(), TenantID: tenantID, Collection: "photos", Key: "k", PublicURL: testPublicBase + "/k"}
	// No state machine: reaching the soft delete would panic.
	h := &Handler{repo: &purgeRepo{obj: obj}, storage: noopStorage{}, policy: &recordingAuthorizer{}, presign: testPresignConfig()}
	err := h.DeleteObject(ctx, "photos", obj.ObjectID.String(), "1", false, false)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v (%v), want FailedPrecondition", connect.CodeOf(err), err)
	}
}

// recordingCopyStorage notes the destination a copy was written to.
type recordingCopyStorage struct {
	publicURLStorage
	dst *Location
}

func (s *recordingCopyStorage) CopyObject(_ context.Context, _, dst Location) error {
	s.dst = &dst
	return nil
}

// A copy into a public collection is named by the server and stored with the
// collection's Cache-Control and the source's content type (ADR-0027).
func TestCopyIntoAPublicCollection(t *testing.T) {
	sm := &fakeStateMachine{changed: true}
	repo := &completeRepo{}
	storage := &recordingCopyStorage{}
	h, _, _, _, ctx, tenantID := tailHandler(t, repo, storage, sm)
	repo.meta = publicMeta()
	src := copySource(tenantID)
	src.ContentType = "image/jpeg"
	repo.obj = src

	if _, err := h.CopyObject(ctx, CopyObjectInput{
		SourceCollection: "src", SourceObjectID: uuid.NewString(), DestCollection: "photos",
	}); err != nil {
		t.Fatalf("CopyObject: %v", err)
	}
	if !publicKeyShape.MatchString(repo.created.Key) {
		t.Errorf("copy key %q is not one the server names a public object with", repo.created.Key)
	}
	if want := testPublicBase + "/" + tenantID.String() + "/photos/" + repo.created.Key; repo.created.PublicURL != want {
		t.Errorf("public url = %q, want %q", repo.created.PublicURL, want)
	}
	if storage.dst == nil || storage.dst.Key != repo.created.Key ||
		storage.dst.CacheControl != publicread.DefaultCacheControl || storage.dst.ContentType != "image/jpeg" {
		t.Errorf("copied to %+v, want the new key with the collection's cache control and the source's type", storage.dst)
	}
}

func TestCopyIntoAPublicCollectionRefusesAClientKey(t *testing.T) {
	repo := &completeRepo{obj: availableSource()}
	repo.meta = publicMeta()
	h, _, ctx, _ := completeHandler(repo, &publicURLStorage{})
	src := availableSource()
	src.ContentType = "image/jpeg"
	repo.obj = src
	_, err := h.CopyObject(ctx, CopyObjectInput{
		SourceCollection: "src", SourceObjectID: uuid.NewString(), DestCollection: "photos", DestKey: "mine.jpg",
	})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || repo.created != nil {
		t.Errorf("err = %v, created %+v; want FailedPrecondition before any row", err, repo.created)
	}
}
