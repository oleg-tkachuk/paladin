package wire

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/bucketh"
)

// The bucket handler's provisioner must reach it exactly as wired — nil
// included.
//
// It used to arrive wrapped. `bucketProvisionerAdapter` bridged
// bucket.Provisioner to bucketh.Provisioner, two interfaces that declare the
// same two methods, which Go already treats as interchangeable — so the
// wrapper was ceremony. Its nil handling was not: both methods returned nil
// when the wrapped value was nil, which is indistinguishable from "the bucket
// was provisioned". bucketh has an answer for a missing provisioner —
// CodeUnavailable, "backend provisioning not wired", guarded on all three of
// its provisioning paths and covered by its own tests — and the wrapper made
// every one of them unreachable, because the handler always held a non-nil
// provisioner.
//
// Nothing failed. A deployment with no provisioner would have been told its
// buckets were provisioned, and only the absence of the S3 bucket would ever
// have said otherwise.
//
// Two checks, because the property has two halves.

// TestBucketProvisionerNilSurvivesTheInterfaceConversion pins the language
// rule the fix leans on: a nil interface value converted to another interface
// type stays nil, so bucketh's `provisioner == nil` guard can still fire.
// (A typed nil pointer stored in an interface would not — that is a different
// bug, and not one this wiring can introduce, since it passes an interface
// value straight through.)
func TestBucketProvisionerNilSurvivesTheInterfaceConversion(t *testing.T) {
	var storage Storage // Provisioner left unset
	var p bucketh.Provisioner = storage.Provisioner
	if p != nil {
		t.Fatal("an unwired provisioner arrived non-nil; bucketh would report " +
			"a bucket as provisioned instead of answering Unavailable")
	}
}

// TestBucketProvisionerIsPassedUnwrapped is the half the type system cannot
// state: that the value handed to bucketh.NewHandler is `storage.Provisioner`
// itself and not something built around it. Any wrapper compiles and passes
// every other test — that is how the original got in — so the guard has to be
// on the shape of the call.
func TestBucketProvisionerIsPassedUnwrapped(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "wire.go", nil, 0)
	if err != nil {
		t.Fatalf("parse wire.go: %v", err)
	}

	var arg ast.Expr
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "ProvideBucketV2Handler" {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "NewHandler" || len(call.Args) < 2 {
				return true
			}
			arg = call.Args[1]
			return false
		})
		return false
	})

	if arg == nil {
		t.Fatal("no bucketh.NewHandler call found in ProvideBucketV2Handler — " +
			"if the wiring moved, move this check with it")
	}
	sel, ok := arg.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Provisioner" {
		t.Fatalf("the provisioner argument is %T, not storage.Provisioner — "+
			"a wrapper here can turn a missing provisioner into a silent "+
			"success, which is what this guards", arg)
	}
	if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "storage" {
		t.Fatalf("provisioner argument reads %v.Provisioner, want storage.Provisioner", sel.X)
	}
}
