package config

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// secretFieldName matches the yaml names that hold a credential once the
// resolver has run; a bare "key" too, which capability.dpop_nonce.key is. Obfuscated() is a hand-kept list, and it has missed a new
// one more than once; this sweep finds the next before a boot log prints it.
var secretFieldName = regexp.MustCompile(`(password|secret|token|_key|^key)$`)

// notSecret lists yaml paths the pattern matches that hold no credential.
var notSecret = map[string]bool{
	// The name of the entry inside a Secret, not its value.
	"ingest.webhook.shared_secret_ref.key": true,
	"ingest.nats.token_ref.key":            true,
	"ingest.rabbitmq.url_ref.key":          true,
}

const plantedPrefix = "planted-secret:"

// walkStrings calls fn for every settable string field under v, with its
// dotted yaml path. Maps of structs are walked through a copy written back;
// a slice of structs gets one element, walked as "[]".
func walkStrings(v reflect.Value, path string, fn func(path string, f reflect.Value)) {
	switch v.Kind() {
	case reflect.Struct:
		t := v.Type()
		for i := range t.NumField() {
			sf := t.Field(i)
			if !sf.IsExported() {
				continue
			}
			name := strings.Split(sf.Tag.Get("yaml"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			walkStrings(v.Field(i), joinPath(path, name), fn)
		}
	case reflect.String:
		fn(path, v)
	case reflect.Map:
		if v.Type().Elem().Kind() != reflect.Struct {
			return
		}
		if v.IsNil() {
			v.Set(reflect.MakeMap(v.Type()))
		}
		key := reflect.New(v.Type().Key()).Elem()
		elem := reflect.New(v.Type().Elem()).Elem()
		if existing := v.MapIndex(key); existing.IsValid() {
			elem.Set(existing)
		}
		walkStrings(elem, path+".*", fn)
		v.SetMapIndex(key, elem)
	case reflect.Slice:
		if v.Type().Elem().Kind() != reflect.Struct {
			return
		}
		if v.Len() == 0 {
			v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		}
		walkStrings(v.Index(0), path+"[]", fn)
	}
}

func joinPath(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "." + name
}

func TestObfuscated_RedactsEverySecretShapedField(t *testing.T) {
	var c Config
	planted := 0
	walkStrings(reflect.ValueOf(&c).Elem(), "", func(path string, f reflect.Value) {
		leaf := path[strings.LastIndex(path, ".")+1:]
		if secretFieldName.MatchString(leaf) && !notSecret[path] {
			f.SetString(plantedPrefix + path)
			planted++
		}
	})
	if planted == 0 {
		t.Fatal("no secret-shaped field found; the sweep is asserting nothing")
	}

	o := c.Obfuscated()
	walkStrings(reflect.ValueOf(&o).Elem(), "", func(path string, f reflect.Value) {
		if strings.HasPrefix(f.String(), plantedPrefix) {
			t.Errorf("%s is not redacted by Obfuscated(); add it there, or to notSecret if it holds no credential", path)
		}
	})
}
