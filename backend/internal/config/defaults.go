package config

import (
	"encoding/json"
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	goyaml "gopkg.in/yaml.v3"
)

// Defaults is the configuration a deployment runs on where it sets nothing:
// every field schema.cue gives a concrete default, as Load would fill it in.
// A field with no default, or one whose default is another field the
// deployment must set (logger.fields.service is app.name), is left at its
// zero value; read beside the running configuration, the difference is what
// the deployment changed.
func Defaults() (Config, error) {
	schema := cuecontext.New().CompileString(cueSchema)
	if schema.Err() != nil {
		return Config{}, fmt.Errorf("CUE schema invalid: %w", schema.Err())
	}
	tree, err := schemaDefaults(schema)
	if err != nil {
		return Config{}, err
	}
	raw, err := json.Marshal(tree)
	if err != nil {
		return Config{}, fmt.Errorf("config defaults: %w", err)
	}
	var cfg Config
	if err := goyaml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("config defaults: %w", err)
	}
	return cfg, nil
}

// schemaDefaults is the tree of v's concrete defaults: a struct's fields in
// turn, a leaf where CUE knows its default. A struct with no default under
// it is left out.
func schemaDefaults(v cue.Value) (map[string]any, error) {
	fields, err := v.Fields(cue.Optional(true))
	if err != nil {
		return nil, fmt.Errorf("config defaults: %w", err)
	}
	out := map[string]any{}
	for fields.Next() {
		name, field := fields.Selector().Unquoted(), fields.Value()
		if field.IncompleteKind() == cue.StructKind {
			sub, err := schemaDefaults(field)
			if err != nil {
				return nil, err
			}
			if len(sub) > 0 {
				out[name] = sub
			}
			continue
		}
		d, ok := field.Default()
		if !ok || d.Validate(cue.Concrete(true)) != nil {
			continue
		}
		var leaf any
		if err := d.Decode(&leaf); err != nil {
			return nil, fmt.Errorf("config defaults: %s: %w", fields.Selector(), err)
		}
		out[name] = leaf
	}
	return out, nil
}
