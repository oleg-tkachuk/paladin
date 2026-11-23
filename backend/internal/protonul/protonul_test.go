package protonul

import (
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
)

// withNUL is a string holding NUL between printable text.
const withNUL = "a" + string(NUL) + "b"

// deepLevels is how deep the nested case goes: deeper than any request the
// contract declares.
const deepLevels = 40

// messagesPerStructLevel is how many messages one level of deepValue nests:
// a Value, the Struct it holds, and the Struct's map entry.
const messagesPerStructLevel = 3

// decoderLevels nests deepValue as deep as the wire decoder admits a message
// to be, which is as deep as a request reaching the walker can be.
const decoderLevels = protowire.DefaultRecursionLimit / messagesPerStructLevel

// largeEntries is the size of the large case's map: thousands of entries, as
// a maximum-size BatchUpdateTags carries.
const largeEntries = 4000

func TestFind(t *testing.T) {
	cases := []struct {
		name string
		msg  proto.Message
		// path is where the NUL is; empty when there is none.
		path string
	}{
		{"an empty message", &datav1.UploadObjectRequest{}, ""},
		{"clean strings", &datav1.UploadObjectRequest{
			Parent: "tenants/t/collections/c", Key: "a/b", Metadata: map[string]string{"k": "v"},
		}, ""},
		{"a singular string", &datav1.UploadObjectRequest{Parent: "p", Key: withNUL}, "key"},
		{"NUL alone", &datav1.UploadObjectRequest{Key: string(NUL)}, "key"},
		{"NUL at the end", &datav1.UploadObjectRequest{ExternalRef: "ref" + string(NUL)}, "external_ref"},
		{"the first field in declaration order", &datav1.UploadObjectRequest{Parent: withNUL, Key: withNUL}, "parent"},
		{"a map value", &datav1.UploadObjectRequest{Metadata: map[string]string{"k": withNUL}}, "metadata.value"},
		{"a map key", &datav1.UploadObjectRequest{Tags: map[string]string{withNUL: "v"}}, "tags.key"},
		{"a repeated string", &datav1.DismissOperationsRequest{Names: []string{"a", "b", withNUL}}, "names[2]"},
		{"a wrapper", wrapperspb.String(withNUL), "value"},
		{"bytes are skipped", wrapperspb.Bytes([]byte(withNUL)), ""},
		{"a oneof", structpb.NewStringValue(withNUL), "string_value"},
		{"a oneof set to a message", structpb.NewListValue(&structpb.ListValue{
			Values: []*structpb.Value{structpb.NewNumberValue(1), structpb.NewStringValue(withNUL)},
		}), "list_value.values[1].string_value"},
		{"a nested message in a request", &iamv1.UpdateMineRequest{Preferences: mustStruct(t, map[string]any{"ui": withNUL})},
			"preferences.fields.value.string_value"},
		{"a nested map key in a request", &iamv1.UpdateMineRequest{Preferences: mustStruct(t, map[string]any{withNUL: 1})},
			"preferences.fields.key"},
		{"a field mask path", &iamv1.UpdateMineRequest{UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"theme", withNUL}}},
			"update_mask.paths[1]"},
		{"an Any's type URL", &anypb.Any{TypeUrl: withNUL}, "type_url"},
		{"an Any's packed bytes are skipped", &anypb.Any{TypeUrl: "type.googleapis.com/x", Value: []byte(withNUL)}, ""},
		{"deep nesting", deepValue(deepLevels, withNUL), deepPath(deepLevels)},
		{"deep nesting, clean", deepValue(deepLevels, "clean"), ""},
		{"nesting as deep as the decoder admits", deepValue(decoderLevels, withNUL), deepPath(decoderLevels)},
		{"a large message", largeBatch(withNUL), "tags.value"},
		{"a large message, clean", largeBatch("v"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Find(tc.msg.ProtoReflect())
			switch {
			case tc.path == "" && got != nil:
				t.Fatalf("Find = %q, want none", got.Path)
			case tc.path == "":
				return
			case got == nil:
				t.Fatalf("Find = none, want %q", tc.path)
			case got.Path != tc.path:
				t.Fatalf("Find = %q, want %q", got.Path, tc.path)
			}
			if want := fmt.Sprintf(RefusalFormat, tc.path); got.Error() != want {
				t.Errorf("Error() = %q, want %q", got.Error(), want)
			}
			if strings.ContainsRune(got.Error(), NUL) {
				t.Errorf("Error() holds the NUL it reports: %q", got.Error())
			}
		})
	}
}

// A nil message, as a typed nil reaches a walker, holds nothing.
func TestFindNilMessage(t *testing.T) {
	var m *datav1.UploadObjectRequest
	if got := Find(m.ProtoReflect()); got != nil {
		t.Fatalf("Find(nil) = %q, want none", got.Path)
	}
}

// The error is a refusal by the failure log's contract, which it meets
// without importing the package that declares it.
func TestErrorIsARefusal(t *testing.T) {
	var err error = &Error{Path: "key"}
	if _, ok := err.(interface{ Refuses() }); !ok {
		t.Fatal("*Error does not mark itself a refusal")
	}
}

func mustStruct(t *testing.T, fields map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(fields)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// deepValue is a Value nested levels deep in single-field Structs, with leaf
// as the innermost string.
func deepValue(levels int, leaf string) *structpb.Value {
	v := structpb.NewStringValue(leaf)
	for range levels {
		v = structpb.NewStructValue(&structpb.Struct{Fields: map[string]*structpb.Value{"n": v}})
	}
	return v
}

// deepPath is where deepValue keeps its leaf.
func deepPath(levels int) string {
	return strings.Repeat("struct_value.fields.value.", levels) + "string_value"
}

// largeBatch is a BatchUpdateTagsRequest of largeEntries clean tags, and one
// more whose value is last.
func largeBatch(last string) *datav1.BatchUpdateTagsRequest {
	tags := make(map[string]string, largeEntries+1)
	for i := range largeEntries {
		tags[fmt.Sprintf("k%06d", i)] = strings.Repeat("v", 64)
	}
	tags["last"] = last
	return &datav1.BatchUpdateTagsRequest{Parent: "tenants/t/collections/c", Tags: tags}
}

// The clean path is the one every request takes; it names nothing, so it
// allocates nothing of its own. Lists and maps are left out: the protobuf
// runtime boxes their elements as it hands them out, which no walker over
// protoreflect avoids.
func TestFindCleanAllocatesNothing(t *testing.T) {
	m := (&datav1.UploadObjectRequest{Parent: "tenants/t/collections/c", Key: "a/b", ContentType: "text/plain"}).ProtoReflect()
	if allocs := testing.AllocsPerRun(10, func() { _ = Find(m) }); allocs != 0 {
		t.Errorf("Find on a clean message allocated %v times, want 0", allocs)
	}
}

func BenchmarkFindLargeClean(b *testing.B) {
	m := largeBatch("v").ProtoReflect()
	b.ReportAllocs()
	for b.Loop() {
		_ = Find(m)
	}
}
