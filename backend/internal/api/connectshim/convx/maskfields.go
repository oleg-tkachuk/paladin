package convx

import "google.golang.org/protobuf/reflect/protoreflect"

// UnknownMaskFields returns the paths that name no field of msg. A shim's
// supported-path list is checked against the message it updates with this, so
// a typo in the list — the drift CheckMask exists to stop — cannot hide in it.
func UnknownMaskFields(msg protoreflect.MessageDescriptor, paths []string) []string {
	var unknown []string
	for _, p := range paths {
		if msg.Fields().ByName(protoreflect.Name(p)) == nil {
			unknown = append(unknown, p)
		}
	}
	return unknown
}
