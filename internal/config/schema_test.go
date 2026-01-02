package config

import (
	"testing"

	"cuelang.org/go/cue/cuecontext"
)

func TestCueSchema(t *testing.T) {
	ctx := cuecontext.New()
	schemaVal := ctx.CompileString(cueSchema)
	if schemaVal.Err() != nil {
		t.Fatalf("CUE schema invalid: %v", schemaVal.Err())
	}
}
