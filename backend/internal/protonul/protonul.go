// Package protonul finds a NUL character (U+0000) in the strings of a
// protobuf message.
//
// PostgreSQL stores no NUL in a text column, and jsonb refuses \u0000 in a
// string, so a request string holding one fails at the store, long after the
// request was accepted. Find walks a message as the wire decoded it, so the
// server can refuse such a request once, before anything reads it.
//
// Every string is checked: singular and repeated fields, map keys and map
// values, the fields of nested messages and of a set oneof, and well-known
// types such as StringValue and Struct, which are messages like any other.
// Bytes fields are not text and may carry anything. An Any's packed message is
// bytes too and is not unpacked; its type URL is a string and is checked.
//
// The package imports nothing but the protobuf runtime, so the interceptor
// that uses it, and anything else that wants the rule, stays light.
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
	MapKeySegment   = "key"
	MapValueSegment = "value"
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

// Error is a message holding NUL in the string at Path.
type Error struct {
	// Path names the field from the top of the message: field names joined by
	// dots, a list element's index in brackets, a map entry's key or value as
	// "key" or "value" after the map field — tags.value, items[2].name. It
	// holds no value of the message, map keys included.
	Path string
}

func (e *Error) Error() string { return fmt.Sprintf(RefusalFormat, e.Path) }

// Refuses marks the error as a verdict on the request (rpcerr.Refusal): the
// server decided and said no, which the failure log records as a refusal.
func (*Error) Refuses() {}

// Find returns the first string in m holding NUL — fields in declaration
// order, a map's entries in no fixed one — or nil when there is none. It
// allocates nothing of its own but the name of what it found.
func Find(m protoreflect.Message) *Error {
	path, found := findMessage(m)
	if !found {
		return nil
	}
	slices.Reverse(path)
	return &Error{Path: strings.Join(path, "")}
}

// findMessage reports whether m holds NUL, and the path to it in reverse:
// each level adds its own element on the way back up, so a message without
// NUL builds no path.
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
	// Extensions are not in the descriptor's field list; Range reaches them,
	// at the cost of visiting every set field again, so only a message that
	// declares extension ranges pays it.
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

// findField reports whether the value v of fd holds NUL.
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
// names which, never the key itself: the key is the client's own text, and
// may be the very string that holds NUL.
func findMap(fd protoreflect.FieldDescriptor, m protoreflect.Map) ([]string, bool) {
	var path []string
	var found bool
	keyIsString := fd.MapKey().Kind() == protoreflect.StringKind
	m.Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
		if keyIsString && strings.ContainsRune(k.String(), NUL) {
			path, found = []string{MapKeySegment}, true
			return false
		}
		if path, found = findSingular(fd.MapValue(), v); found {
			path = prependPath(path, MapValueSegment)
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
