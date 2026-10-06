package admin

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// eventSinkTargetOneof is the oneof on pb.EventSink that selects the sink.
const eventSinkTargetOneof protoreflect.Name = "target"

// Every sink the API accepts must write a kind the event_sink_kind enum holds,
// or the INSERT rejects it. Walking the oneof by reflection means a new sink
// variant is checked here without anyone remembering to add it.
func TestSinkToConfig_KindsMatchEnum(t *testing.T) {
	enumKinds := map[string]bool{
		string(sqlc.EventSinkKindHttp):     false,
		string(sqlc.EventSinkKindNats):     false,
		string(sqlc.EventSinkKindKafka):    false,
		string(sqlc.EventSinkKindSqs):      false,
		string(sqlc.EventSinkKindRabbitmq): false,
	}

	oneof := (&pb.EventSink{}).ProtoReflect().Descriptor().Oneofs().ByName(eventSinkTargetOneof)
	if oneof == nil {
		t.Fatalf("pb.EventSink has no oneof %q", eventSinkTargetOneof)
	}
	fields := oneof.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		t.Run(string(fd.Name()), func(t *testing.T) {
			m := (&pb.EventSink{}).ProtoReflect()
			m.Set(fd, m.NewField(fd))
			kind, _ := sinkToConfig(m.Interface().(*pb.EventSink))
			if _, ok := enumKinds[kind]; !ok {
				t.Fatalf("sink %s writes kind %q, which event_sink_kind does not hold", fd.Name(), kind)
			}
			enumKinds[kind] = true

			// And it reads back as the same variant.
			back := sinkFromConfig(kind, nil)
			if got := back.ProtoReflect().WhichOneof(oneof); got == nil || got.Name() != fd.Name() {
				t.Errorf("kind %q reads back as %v, want %s", kind, got, fd.Name())
			}
		})
	}
	for kind, produced := range enumKinds {
		if !produced {
			t.Errorf("enum kind %q is produced by no sink variant", kind)
		}
	}
}
