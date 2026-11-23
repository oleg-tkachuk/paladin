package policies

import (
	"errors"
	"io/fs"
	"path"
	"strings"
	"testing"
)

func TestSchemaResolves(t *testing.T) {
	if _, err := Resolve(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsWhatTheSchemaDoesNotDeclare(t *testing.T) {
	for _, tc := range []struct{ name, text string }{
		{"unknown action", `permit (principal, action == Action::"ManageObjectKey", resource);`},
		{"unknown attribute", `permit (principal, action == Action::"ReadTenant", resource) when { principal.parents == principal.parents };`},
		{"unguarded key on a Collection", `permit (principal, action == Action::"PutObject", resource) when { resource.key like "*.exe" };`},
		{"unparseable", `permit (`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := Validate(tc.name, []byte(tc.text)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestValidateAcceptsAGuardedRead(t *testing.T) {
	text := `permit (principal, action == Action::"PutObject", resource)
when { resource has key && resource.key like "*.pdf" };`
	if err := Validate("guarded", []byte(text)); err != nil {
		t.Fatal(err)
	}
}

func TestExamplePoliciesValidate(t *testing.T) {
	files, err := fs.Glob(Examples, path.Join(ExamplesDir, "*.cedar"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no example policies: %v", err)
	}
	for _, f := range files {
		text, err := fs.ReadFile(Examples, f)
		if err != nil {
			t.Fatal(err)
		}
		if err := Validate(f, text); err != nil {
			t.Error(err)
		}
	}
}

func TestCheckSeparatesUnparseableFromFindings(t *testing.T) {
	if _, err := Check("bad", []byte(`permit (`)); !errors.Is(err, ErrUnparseable) {
		t.Fatalf("err = %v, want ErrUnparseable", err)
	}
	two := `permit (principal, action == Action::"ReadTenant", resource) when { principal.parents == principal.parents };
permit (principal, action == Action::"PutObject", resource) when { resource.key like "*.exe" };
permit (principal, action == Action::"ReadTenant", resource);`
	findings, err := Check("two", []byte(two))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %q, want one per policy that does not type-check", findings)
	}
}

// A policy with several type errors is still one finding, on one line: the
// validator joins them with a newline, which reached the console raw.
func TestCheckPutsAPolicysErrorsOnOneLine(t *testing.T) {
	findings, err := Check("one", []byte(`permit(principal, action, resource) when { resource.nope == 1 };`))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %q, want one for the one policy", findings)
	}
	if strings.Contains(findings[0], "\n") || strings.Count(findings[0], findingSeparator) == 0 {
		t.Errorf("finding = %q, want its errors on one line, joined by %q", findings[0], findingSeparator)
	}
}
