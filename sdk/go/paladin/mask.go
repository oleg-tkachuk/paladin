package paladin

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// ErrUnknownMaskPath is returned by Mask for a path the message does not have.
var ErrUnknownMaskPath = errors.New("paladin: no such field for the update mask")

// maskPathSeparator separates the fields of a nested mask path.
const maskPathSeparator = "."

// Mask builds an update mask for M from proto field names, checking each
// against M's descriptor: the server refuses a path it does not know, and a
// misspelt path otherwise reaches it as a request that changes nothing.
//
//	mask, err := paladin.Mask[*adminv1.EventSubscription]("filter", "sink")
func Mask[M proto.Message](paths ...string) (*fieldmaskpb.FieldMask, error) {
	var m M
	desc := m.ProtoReflect().Descriptor()
	for _, path := range paths {
		d := desc
		for i, name := range strings.Split(path, maskPathSeparator) {
			if d == nil {
				return nil, fmt.Errorf("%w: %q (%s is not a message)", ErrUnknownMaskPath, path,
					strings.Join(strings.Split(path, maskPathSeparator)[:i], maskPathSeparator))
			}
			fd := d.Fields().ByName(protoreflect.Name(name))
			if fd == nil {
				return nil, fmt.Errorf("%w: %q on %s", ErrUnknownMaskPath, path, desc.FullName())
			}
			d = fd.Message()
		}
	}
	return &fieldmaskpb.FieldMask{Paths: append([]string(nil), paths...)}, nil
}
