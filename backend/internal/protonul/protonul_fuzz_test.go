package protonul

import (
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"

	"github.com/oleg-tkachuk/paladin/backend/tests/fuzzassert"
)

// longSeedBytes is the length of the seed whose NUL comes after a long run of
// clean text.
const longSeedBytes = 1 << 10

// slot is one place a fuzzed string is put: the message holding it there, and
// the path a refusal names, or "" for a place that is not text.
type slot struct {
	build func(s string) proto.Message
	path  string
}

var slots = []slot{
	{func(s string) proto.Message { return &datav1.UploadObjectRequest{Parent: "p", Key: s} }, "key"},
	{func(s string) proto.Message { return &datav1.UploadObjectRequest{Metadata: map[string]string{"k": s}} }, "metadata.value"},
	{func(s string) proto.Message { return &datav1.UploadObjectRequest{Tags: map[string]string{s: "v"}} }, "tags.key"},
	{func(s string) proto.Message { return &datav1.DismissOperationsRequest{Names: []string{"a", s}} }, "names[1]"},
	{func(s string) proto.Message { return wrapperspb.String(s) }, "value"},
	{func(s string) proto.Message {
		return &iamv1.UpdateMineRequest{Preferences: &structpb.Struct{Fields: map[string]*structpb.Value{
			"ui": structpb.NewListValue(&structpb.ListValue{Values: []*structpb.Value{structpb.NewStringValue(s)}}),
		}}}
	}, "preferences.fields.value.list_value.values[0].string_value"},
	{func(s string) proto.Message { return &anypb.Any{TypeUrl: s} }, "type_url"},
	{func(s string) proto.Message { return wrapperspb.Bytes([]byte(s)) }, ""},
	{func(s string) proto.Message { return &anypb.Any{TypeUrl: "type.googleapis.com/x", Value: []byte(s)} }, ""},
}

// Find refuses a string exactly when it holds NUL and sits where text goes,
// and its refusal is the fixed sentence naming that place — printable, and
// never the string itself.
func FuzzFind(f *testing.F) {
	for i := range slots {
		for _, s := range []string{"", "clean", withNUL, string(NUL), "key", "\xff", "a\u202eb", strings.Repeat("x", longSeedBytes) + string(NUL)} {
			f.Add(s, uint8(i))
		}
	}
	f.Fuzz(func(t *testing.T, s string, which uint8) {
		sl := slots[int(which)%len(slots)]
		got := Find(sl.build(s).ProtoReflect())
		refuse := sl.path != "" && strings.ContainsRune(s, NUL)
		switch {
		case !refuse && got != nil:
			t.Fatalf("Find(%q in %q) = %q, want none", s, sl.path, got.Path)
		case !refuse:
			return
		case got == nil:
			t.Fatalf("Find(%q in %q) = none, want a refusal", s, sl.path)
		}
		if want := fmt.Sprintf(RefusalFormat, sl.path); got.Error() != want {
			t.Fatalf("Find(%q) = %q, want %q", s, got.Error(), want)
		}
		fuzzassert.Text(t, got.Error())
	})
}
