package middleware

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// redacted returns a copy of m with every field marked `debug_redact` in the
// proto cleared, at any depth. Used wherever a request or response is stored:
// the audit row (which kept refresh tokens and initial passwords) and the
// idempotency cache (which kept minted API and capability tokens).
func redacted(m proto.Message) proto.Message {
	c := proto.Clone(m)
	clearRedacted(c.ProtoReflect())
	return c
}

func clearRedacted(m protoreflect.Message) {
	// Cleared after the walk: Range does not promise a message may change
	// under it.
	var clear []protoreflect.FieldDescriptor
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if isRedacted(fd) {
			clear = append(clear, fd)
			return true
		}
		switch {
		case fd.IsList() && fd.Message() != nil:
			list := v.List()
			for i := range list.Len() {
				clearRedacted(list.Get(i).Message())
			}
		case fd.IsMap() && fd.MapValue().Message() != nil:
			v.Map().Range(func(_ protoreflect.MapKey, mv protoreflect.Value) bool {
				clearRedacted(mv.Message())
				return true
			})
		case fd.Message() != nil && !fd.IsList() && !fd.IsMap():
			clearRedacted(v.Message())
		}
		return true
	})
	for _, fd := range clear {
		m.Clear(fd)
	}
}

func isRedacted(fd protoreflect.FieldDescriptor) bool {
	opts, ok := fd.Options().(*descriptorpb.FieldOptions)
	return ok && opts.GetDebugRedact()
}

// carriesCredentials reports whether messages of type md can hold a field
// marked debug_redact, at any depth.
func carriesCredentials(md protoreflect.MessageDescriptor) bool {
	return carriesCredentialsSeen(md, map[protoreflect.FullName]bool{})
}

func carriesCredentialsSeen(md protoreflect.MessageDescriptor, seen map[protoreflect.FullName]bool) bool {
	if seen[md.FullName()] {
		return false
	}
	seen[md.FullName()] = true
	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if isRedacted(fd) {
			return true
		}
		if fd.IsMap() {
			fd = fd.MapValue()
		}
		if fd.Message() != nil && carriesCredentialsSeen(fd.Message(), seen) {
			return true
		}
	}
	return false
}
