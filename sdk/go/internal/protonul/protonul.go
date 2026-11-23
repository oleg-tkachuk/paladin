// Package protonul finds a NUL character (U+0000) in the strings of a
// protobuf message, as the server does before it serves a request: the
// server refuses such a request as InvalidArgument, naming the field, and
// the SDK's fake does the same with this package.
//
// The server's own walker lives in its module, which the SDK cannot import;
// sdk/testdata/request_nul.json holds both, and the Python SDK's fake, to the
// same paths.
//
// Every string is checked: singular and repeated fields, map keys and map
// values, the fields of nested messages and of a set oneof, and well-known
// types such as StringValue and Struct, which are messages like any other.
// Bytes fields are not text and may carry anything.
package protonul

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// NUL is the character no request string may hold.
const NUL = '\x00'

// RefusalFormat is the refusal's text: the path of the field that holds NUL.
// The value itself is never part of it.
const RefusalFormat = "%s: holds a NUL character"

// The path elements a map entry's two halves are named by, after the map
// field: the names protobuf gives a map entry's fields.
const (
	mapKeySegment   = "key"
	mapValueSegment = "value"
)

// The punctuation of a path: a dot between nested fields, brackets around a
// list index, parentheses around an extension's full name.
const (
	pathSeparator  = "."
	indexOpen      = "["
	indexClose     = "]"
	extensionOpen  = "("
	extensionClose = ")"
)

// Find is the path of the first string in m that holds NUL, fields in
// declaration order, and false when none does.
func Find(m protoreflect.Message) (string, bool) {
	path, found := findMessage(m)
	if !found {
		return "", false
	}
	slices.Reverse(path)
	return strings.Join(path, ""), true
}

// Refusal is the refusal's text for a NUL at path.
func Refusal(path string) string { return fmt.Sprintf(RefusalFormat, path) }

// findMessage walks m's set fields, then its extensions. The path comes back
// reversed, so each level appends rather than copies.
func findMessage(m protoreflect.Message) ([]string, bool) {
	if !m.IsValid() {
		return nil, false
	}
	fields := m.Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if !m.Has(fd) {
			continue
		}
		if path, found := findField(fd, m.Get(fd)); found {
			return prependPath(path, fieldName(fd)), true
		}
	}
	if m.Descriptor().ExtensionRanges().Len() == 0 {
		return nil, false
	}
	var path []string
	var found bool
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if !fd.IsExtension() {
			return true
		}
		if path, found = findField(fd, v); found {
			path = prependPath(path, fieldName(fd))
		}
		return !found
	})
	return path, found
}

// findField walks one set field: a map, a list, or a single value.
func findField(fd protoreflect.FieldDescriptor, v protoreflect.Value) ([]string, bool) {
	switch {
	case fd.IsMap():
		return findMap(fd, v.Map())
	case fd.IsList():
		list := v.List()
		for i := range list.Len() {
			if path, found := findSingular(fd, list.Get(i)); found {
				return prependPath(path, indexOpen+strconv.Itoa(i)+indexClose), true
			}
		}
		return nil, false
	default:
		return findSingular(fd, v)
	}
}

// findMap reports whether a key or a value of the map field fd holds NUL. It
// names which, never the key itself, which may be the string holding NUL.
func findMap(fd protoreflect.FieldDescriptor, m protoreflect.Map) ([]string, bool) {
	var path []string
	var found bool
	keyIsString := fd.MapKey().Kind() == protoreflect.StringKind
	m.Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
		if keyIsString && strings.ContainsRune(k.String(), NUL) {
			path, found = []string{mapKeySegment}, true
			return false
		}
		if path, found = findSingular(fd.MapValue(), v); found {
			path = prependPath(path, mapValueSegment)
		}
		return !found
	})
	return path, found
}

// findSingular reports whether one value of fd's kind holds NUL: a string
// itself, or a message's fields. Every other kind, bytes included, holds no
// text.
func findSingular(fd protoreflect.FieldDescriptor, v protoreflect.Value) ([]string, bool) {
	switch fd.Kind() {
	case protoreflect.StringKind:
		return nil, strings.ContainsRune(v.String(), NUL)
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return findMessage(v.Message())
	default:
		return nil, false
	}
}

// prependPath puts elem in front of a path held in reverse. A dot separates
// it from the element that follows, unless that is a list index, which
// follows its field directly.
func prependPath(path []string, elem string) []string {
	if len(path) > 0 && !strings.HasPrefix(path[len(path)-1], indexOpen) {
		path = append(path, pathSeparator)
	}
	return append(path, elem)
}

// fieldName is fd's name as a path names it: an extension by its full name in
// parentheses, as an option is named in a .proto file.
func fieldName(fd protoreflect.FieldDescriptor) string {
	if fd.IsExtension() {
		return extensionOpen + string(fd.FullName()) + extensionClose
	}
	return string(fd.Name())
}
