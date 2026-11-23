package objecth

import (
	"context"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/checksum"
	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/keyset"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/uploadpolicy"
	"github.com/oleg-tkachuk/paladin/backend/tests/fuzzassert"
)

// The handler-level fuzz targets drive the RPC bodies over fakes that never
// fail, so an error is the handler's own answer to the input.

// uploadCodes is what UploadObject answers a malformed upload with: a key,
// checksum, size or content type it refuses.
var uploadCodes = []connect.Code{connect.CodeInvalidArgument}

// Whatever an upload names, UploadObject either signs it or refuses it as the
// caller's mistake, in words that name nothing of the server.
func FuzzUploadObject(f *testing.F) {
	for _, c := range []struct {
		collection, key, contentType, algo, value string
		size                                      int64
		post                                      bool
	}{
		{"docs", "a.txt", "text/plain", checksum.SHA256, testChecksumValue, 5, false},
		{"docs", "", "text/plain; charset=utf-8", "SHA256", testChecksumValue, 0, true},
		{"docs", "a//b", "text/plain", checksum.SHA256, testChecksumValue, 1, false},
		{"docs", "..", "image/png", "CRC32C", "AAAAAA==", 1, false},
		{"docs", "a\nb", "application/x-msdownload", "MD5", "x", -1, false},
		{"invoices/2026", "dir/", "", "", "", 1 << 62, true},
	} {
		f.Add(c.collection, c.key, c.contentType, c.algo, c.value, c.size, c.post, "k", "v")
	}
	f.Fuzz(func(t *testing.T, collection, key, contentType, algo, value string, size int64, post bool, tagKey, tagValue string) {
		h, _, _, ctx := limitsHandler(uploadpolicy.BucketConstraints{}, testUploadLimits)
		in := UploadObjectInput{
			Collection: collection, Key: key, ContentType: contentType, SizeBytes: size,
			ChecksumAlgo: algo, ChecksumValue: value, TransportPOST: post,
			Tags: map[string]string{tagKey: tagValue}, Metadata: map[string]string{tagKey: tagValue},
		}
		_, err := h.UploadObject(ctx, in)
		fuzzassert.Refusal(t, err, uploadCodes, collection, key, contentType, algo, value, tagKey, tagValue)
	})
}

// fuzzListRepo answers a listing as the Postgres adapter does with what it
// reads from the caller: it decodes the page token for the order asked and
// evaluates the compiled filter on each row of a fixed page.
type fuzzListRepo struct {
	fakeObjectRepo
}

var fuzzListPage = []Object{
	{Key: "a/b.txt", State: statemachine.StateAvailable, ContentType: "text/plain", SizeBytes: 11, Tags: map[string]string{"env": "prod"}},
	{Key: "c", State: statemachine.StatePending},
}

func (r *fuzzListRepo) ListObjects(_ context.Context, args ListObjectsArgs) ([]Object, string, error) {
	if args.PageToken != "" {
		if _, err := keyset.Decode(args.PageToken, args.Order); err != nil {
			return nil, "", err
		}
	}
	out := make([]Object, 0, len(fuzzListPage))
	for _, o := range fuzzListPage {
		o.ObjectID = uuid.NewV7()
		if ok, err := cel.Match(args.CompiledCEL, celRow(o)); err == nil && ok {
			out = append(out, o)
		}
	}
	return out, "", nil
}

func (r *fuzzListRepo) CountObjects(context.Context, CountObjectsArgs) (int64, bool, error) {
	return int64(len(fuzzListPage)), true, nil
}

func (r *fuzzListRepo) LookupBucket(context.Context, uuid.UUID, string, bucketgate.Access) (string, string, error) {
	return "backend-list", "bucket-list", nil
}

// celRow is the subset of a row's CEL variables the page's objects carry.
func celRow(o Object) map[string]any {
	return map[string]any{
		"key": o.Key, "state": string(o.State), "content_type": o.ContentType,
		"size_bytes": o.SizeBytes, "tags": o.Tags,
	}
}

// listCodes is what a listing answers a malformed request with: an order,
// delimiter, filter, facet or page token it cannot use.
var listCodes = []connect.Code{connect.CodeInvalidArgument}

// Whatever filter, facets, order, folder level and page token a listing
// names, ListObjects, CountObjects and ListCommonPrefixes answer it or refuse
// it as the caller's mistake, in words that name nothing of the server.
func FuzzListObjects(f *testing.F) {
	token := keyset.Encode(keyset.Cursor{Order: keyset.Order{Field: orderByID}, ID: uuid.NewV7()})
	for _, c := range []struct {
		filter, order, token, prefix, delimiter, state, tagKey, tagValue string
		desc                                                             bool
	}{
		{"", "", "", "", "", "", "", "", false},
		{`state == "AVAILABLE" && size_bytes > 1`, OrderBySize, "", "a/", "/", "AVAILABLE", "env", "prod", true},
		{`state == "available"`, "", token, "", "→", "", "", "", false},
		{`tags["env"] == "prod"`, "status", "x", "", "//", "nope", "a\"b", "\x00", false},
		{`(`, "", "eyJ9", "", "", "", "", "", true},
		{`[1,2,3].all(x, [1,2,3].all(y, x != y))`, "", token, "", "", "", "", "", true},
	} {
		f.Add(c.filter, c.order, c.desc, c.token, c.prefix, c.delimiter, c.state, c.tagKey, c.tagValue)
	}
	f.Fuzz(func(t *testing.T, filter, orderBy string, desc bool, pageToken, prefix, delimiter, state, tagKey, tagValue string) {
		ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: uuid.New()})
		h := &Handler{repo: &fuzzListRepo{}, policy: perRowAuthorizer{}, filter: cel.NewEvaluator()}
		in := ListObjectsInput{
			Collection: "docs", PageSize: 10, PageToken: pageToken, Filter: filter, OrderBy: orderBy, SortDesc: desc,
			Prefix: prefix, Delimiter: delimiter,
			Facets: ObjectFacets{Categories: []ContentCategory{CategoryOther}},
		}
		if state != "" {
			in.Facets.States = []statemachine.State{statemachine.State(state)}
		}
		if tagKey != "" {
			in.Facets.Tags = []Tag{{Key: tagKey, Value: tagValue}}
		}
		inputs := []string{filter, orderBy, pageToken, prefix, delimiter, state, tagKey, tagValue}

		_, _, err := h.ListObjects(ctx, in)
		fuzzassert.Refusal(t, err, listCodes, inputs...)
		_, err = h.CountObjects(ctx, CountObjectsInput{Collection: in.Collection, Filter: withFacets(filter, in.Facets)})
		fuzzassert.Refusal(t, err, listCodes, inputs...)
		_, _, err = h.ListCommonPrefixes(ctx, in)
		fuzzassert.Refusal(t, err, listCodes, inputs...)
	})
}
