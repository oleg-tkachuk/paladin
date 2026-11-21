package cel

import "testing"

// Every listed name resolves to its schema, and an unknown one to none.
func TestSchemaNamesResolve(t *testing.T) {
	names := SchemaNames()
	if len(names) != len(registered) {
		t.Fatalf("names = %v, want %d", names, len(registered))
	}
	for i, name := range names {
		if s := SchemaByName(name); s != registered[i] {
			t.Errorf("SchemaByName(%q) = %v, want %v", name, s, registered[i])
		}
	}
	if s := SchemaByName("Nope"); s != nil {
		t.Errorf("SchemaByName(unknown) = %v, want nil", s)
	}
	for _, want := range []*Schema{ObjectSchema, UserSchema, AuditLogSchema, EventEnvelopeSchema} {
		if SchemaByName(want.Name) != want {
			t.Errorf("%s does not resolve", want.Name)
		}
	}
}
