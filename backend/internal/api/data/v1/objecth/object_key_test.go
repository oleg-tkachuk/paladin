package objecth

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/checksum"
	"github.com/oleg-tkachuk/paladin/backend/internal/publicread"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/storagepath"
	"github.com/oleg-tkachuk/paladin/backend/internal/uploadpolicy"
)

// nonCanonicalKeys each composed, before the key contract, onto a path
// another key or the collection itself also composed to.
var nonCanonicalKeys = []string{"a//b", "a/./b", "/a/b", "..", "a/../b", "dir/"}

func TestAdmitKeyAnswersTheRuleAndTheReason(t *testing.T) {
	err := AdmitKey(FieldKey, "a//b")
	if connect.CodeOf(err) != connect.CodeInvalidArgument || !errors.Is(err, storagepath.ErrNotCanonical) {
		t.Fatalf("AdmitKey = %v, want InvalidArgument wrapping the refusal", err)
	}
	var ce *connect.Error
	if !errors.As(err, &ce) || !strings.HasPrefix(ce.Message(), FieldKey+": "+storagepath.KeyRule+": ") {
		t.Errorf("message = %q, want the field, the rule and a reason", ce.Message())
	}
	if err := AdmitOptionalKey(FieldKey, ""); err != nil {
		t.Errorf("AdmitOptionalKey(\"\") = %v, want nil: the server names the key", err)
	}
	if err := AdmitKey(FieldKey, ""); err == nil {
		t.Error("AdmitKey(\"\") = nil, want a refusal")
	}
}

// An over-long key is refused where it enters, before a row is written, and
// the refusal names the limit.
func TestAdmitKeyNamesTheLengthLimit(t *testing.T) {
	limit := strconv.Itoa(storagepath.MaxKeyBytes)
	err := AdmitKey(FieldKey, strings.Repeat("a", storagepath.MaxKeyBytes+1))
	var ce *connect.Error
	if !errors.As(err, &ce) || ce.Code() != connect.CodeInvalidArgument || !strings.Contains(ce.Message(), "over the limit of "+limit) {
		t.Fatalf("AdmitKey(over-long) = %v, want InvalidArgument naming the limit %s", err, limit)
	}
	if err := AdmitKeyPrefix(FieldDestinationKeyPrefix, strings.Repeat("a", storagepath.MaxKeyBytes)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("AdmitKeyPrefix(no room for a key) = %v, want InvalidArgument", err)
	}
	if err := AdmitKey(FieldKey, "a\nb"); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("AdmitKey(control character) = %v, want InvalidArgument", err)
	}
}

// The keys the server names itself pass the contract it holds callers to.
func TestServerNamedKeysAreCanonical(t *testing.T) {
	public, err := publicread.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{UploadKey(""), public} {
		if err := storagepath.ValidateKey(key); err != nil {
			t.Errorf("server-named key %q: %v", key, err)
		}
	}
	if got := UploadKey("mine.txt"); got != "mine.txt" {
		t.Errorf("UploadKey kept %q, want the caller's", got)
	}
	if _, err := uuid.Parse(UploadKey("")); err != nil {
		t.Errorf("UploadKey(\"\") is no id: %v", err)
	}
}

func TestUploadObjectRefusesANonCanonicalKey(t *testing.T) {
	for _, key := range nonCanonicalKeys {
		t.Run(key, func(t *testing.T) {
			h, repo, storage, ctx := limitsHandler(uploadpolicy.BucketConstraints{}, testUploadLimits)
			_, err := h.UploadObject(ctx, UploadObjectInput{Collection: "docs", Key: key, ContentType: "text/plain",
				ChecksumAlgo: checksum.SHA256, ChecksumValue: testChecksumValue})
			if connect.CodeOf(err) != connect.CodeInvalidArgument || !errors.Is(err, storagepath.ErrNotCanonical) {
				t.Fatalf("err = %v, want InvalidArgument for a non-canonical key", err)
			}
			if repo.created || storage.put != nil {
				t.Fatal("a row or URL exists for a refused key")
			}
		})
	}
}

func TestCopyObjectRefusesANonCanonicalDestinationKey(t *testing.T) {
	for _, key := range nonCanonicalKeys {
		t.Run(key, func(t *testing.T) {
			repo := &completeRepo{obj: availableSource()}
			h, _, ctx, _ := completeHandler(repo, headStorage{})
			_, err := h.CopyObject(ctx, CopyObjectInput{
				SourceCollection: "src", SourceObjectID: uuid.New().String(), DestCollection: "dst", DestKey: key,
			})
			if connect.CodeOf(err) != connect.CodeInvalidArgument || !errors.Is(err, storagepath.ErrNotCanonical) {
				t.Fatalf("err = %v, want InvalidArgument", err)
			}
			if repo.created != nil {
				t.Fatal("a row exists for a refused destination key")
			}
		})
	}
}

// A copy that keeps its source's key keeps it only if the key is canonical:
// a source from before the contract cannot place its copy at a shared path.
func TestCopyObjectRefusesToInheritANonCanonicalSourceKey(t *testing.T) {
	src := availableSource()
	src.Key = "legacy//key"
	repo := &completeRepo{obj: src}
	h, _, ctx, _ := completeHandler(repo, headStorage{})
	_, err := h.CopyObject(ctx, CopyObjectInput{
		SourceCollection: "src", SourceObjectID: uuid.New().String(), DestCollection: "dst",
	})
	if connect.CodeOf(err) != connect.CodeInvalidArgument || !errors.Is(err, storagepath.ErrNotCanonical) {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
	if repo.created != nil {
		t.Fatal("a row exists for a refused inherited key")
	}
}
