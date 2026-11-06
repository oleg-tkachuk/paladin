package cel

import (
	"reflect"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
	"cel.dev/cel-go/interpreter"
)

// stringMap is a row's string map as a filter reads it: a key the map does
// not hold looks up as the empty string, as a NULL column does, instead of
// raising "no such key". The error made a filter's outcome depend on the
// page: one row lacking the key failed the request for every row on it.
//
// It is an Indexer and a FieldTester rather than a Mapper on purpose. The
// interpreter asks a Mapper's Find both to read a key and to answer has(),
// so a lenient Find would make has() true for every key; through these two
// traits a lookup goes to Get and a presence test to IsSet, which still asks
// the map itself. `in`, size() and the comprehension macros use Contains,
// Size and Iterator, which the wrapped map answers unchanged.
type stringMap struct {
	m traits.Mapper
}

func newStringMap(m map[string]string) stringMap {
	return stringMap{m: types.NewStringStringMap(types.DefaultTypeAdapter, m)}
}

// Get returns the value at key, or the empty string when the map lacks it.
func (s stringMap) Get(key ref.Val) ref.Val {
	if v, found := s.m.Find(key); found {
		return v
	}
	if _, isString := key.(types.String); isString {
		return types.String("")
	}
	return types.NewErr("no such key: %v", key)
}

// IsSet reports whether the map holds key: has(m.k) is a presence test.
func (s stringMap) IsSet(key ref.Val) ref.Val { return s.m.Contains(key) }

func (s stringMap) Contains(key ref.Val) ref.Val     { return s.m.Contains(key) }
func (s stringMap) Size() ref.Val                    { return s.m.Size() }
func (s stringMap) Iterator() traits.Iterator        { return s.m.Iterator() }
func (s stringMap) Type() ref.Type                   { return s.m.Type() }
func (s stringMap) Value() any                       { return s.m.Value() }
func (s stringMap) ConvertToType(t ref.Type) ref.Val { return s.m.ConvertToType(t) }

func (s stringMap) ConvertToNative(t reflect.Type) (any, error) {
	return s.m.ConvertToNative(t)
}

// Equal compares the maps' entries; another stringMap is unwrapped first.
func (s stringMap) Equal(other ref.Val) ref.Val {
	if o, ok := other.(stringMap); ok {
		other = o.m
	}
	return s.m.Equal(other)
}

// rowActivation resolves a row's variables, presenting each string map as a
// stringMap. Wrapping at resolution leaves the caller's row untouched.
type rowActivation map[string]any

func (r rowActivation) ResolveName(name string) (any, bool) {
	v, ok := r[name]
	if m, isStringMap := v.(map[string]string); isStringMap {
		return newStringMap(m), true
	}
	return v, ok
}

func (r rowActivation) Parent() interpreter.Activation { return nil }
