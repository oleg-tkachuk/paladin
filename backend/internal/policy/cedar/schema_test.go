package cedar

import (
	"slices"
	"testing"
	"time"

	cedartypes "github.com/cedar-policy/cedar-go/types"
	"github.com/cedar-policy/cedar-go/x/exp/schema/resolved"
	"github.com/cedar-policy/cedar-go/x/exp/schema/validate"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/policies"
)

// policies/schema.cedarschema is what a policy author reads to learn the
// action names, entity types and attributes. Nothing in the engine reads it,
// so it drifted: after the ObjectKey → Collection rename it still declared
// ManageObjectKey and an ObjectKey entity, and a policy written from it
// matched nothing. These tests hold it to what the engine actually emits.

// tagValuesAttr is the one attribute the schema cannot declare: a Record whose
// keys are the object's tag names. Cedar records are closed, so the schema
// documents it in a comment and entity validation skips it.
const tagValuesAttr = "tag_values"

func loadSchema(t *testing.T) *resolved.Schema {
	t.Helper()
	r, err := policies.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// engineActions is every action the engine can be asked about. Action has no
// exported constructor, so this is the complete set a caller can pass.
func engineActions() []string {
	var out []string
	for _, a := range Actions() {
		out = append(out, a.String())
	}
	return out
}

func TestSchemaDeclaresExactlyTheEngineActions(t *testing.T) {
	s := loadSchema(t)
	engine := engineActions()
	if len(engine) == 0 {
		t.Fatal("the engine declares no actions")
	}

	var declared []string
	for uid := range s.Actions {
		declared = append(declared, string(uid.ID))
	}
	for _, a := range engine {
		if !slices.Contains(declared, a) {
			t.Errorf("the engine authorizes %q; the schema does not declare it", a)
		}
	}
	for _, a := range declared {
		if !slices.Contains(engine, a) {
			t.Errorf("the schema declares %q; the engine never authorizes it", a)
		}
	}
}

// fixtures holds one Resource per entity type resourceUID can select, with
// every field that type carries populated.
func fixtures() map[cedartypes.EntityType]*Resource {
	tenant := uuid.New()
	return map[cedartypes.EntityType]*Resource{
		entityTypeObject: {
			TenantID: tenant, Collection: "invoices", Key: "2026/01.pdf",
			ObjectID: uuid.New(), State: "AVAILABLE", SizeBytes: 1,
			ContentType: "application/pdf", Tags: map[string]string{"category": "finance"},
			BackendID: "primary", BucketName: "shared",
		},
		entityTypeCollection: {
			TenantID: tenant, Collection: "invoices",
			BackendID: "primary", BucketName: "shared",
		},
		entityTypeBucket: {
			TenantID: tenant, BackendID: "primary", BucketName: "shared",
			OwnerTenantID: tenant,
		},
		entityTypeStorageBackend: {BackendID: "primary"},
		entityTypeUser: {
			TenantID: tenant, TargetUserID: uuid.New(), TargetSubject: "someone",
		},
		entityTypeTenant: {TenantID: tenant},
	}
}

func fixturePrincipal(r *Resource) *Principal {
	return &Principal{
		Subject: "caller", TenantID: r.TenantID, TenantSlug: fixtureSlug,
		Kind: "user", Roles: []string{"tenant.admin"}, Scopes: []string{"tenant:" + r.TenantID.String()},
	}
}

const fixtureSlug = "acme"

func TestEngineEntitiesConformToTheSchema(t *testing.T) {
	v := validate.New(loadSchema(t))
	e := &Engine{}
	for typ, r := range fixtures() {
		t.Run(string(typ), func(t *testing.T) {
			if got := e.resourceUID(r, fixtureSlug).Type; got != typ {
				t.Fatalf("fixture selects %s, want %s", got, typ)
			}
			for uid, ent := range e.buildEntities(fixturePrincipal(r), r, fixtureSlug) {
				if ent.Attributes.Len() > 0 {
					attrs := cedartypes.RecordMap{}
					for k, val := range ent.Attributes.All() {
						if k != tagValuesAttr {
							attrs[k] = val
						}
					}
					ent.Attributes = cedartypes.NewRecord(attrs)
				}
				if err := v.Entity(ent); err != nil {
					t.Errorf("%s: %v", uid, err)
				}
			}
		})
	}
}

// Every action, against every resource type the schema says it applies to,
// with the context the engine sends.
func TestEngineRequestsConformToTheSchema(t *testing.T) {
	s := loadSchema(t)
	v := validate.New(s)
	e := &Engine{}
	fx := fixtures()
	ctx := buildContext(RequestContext{Now: time.Now()})
	for uid, a := range s.Actions {
		if a.AppliesTo == nil {
			t.Errorf("%s applies to nothing", uid)
			continue
		}
		for _, typ := range a.AppliesTo.Resources {
			r, ok := fx[typ]
			if !ok {
				t.Errorf("%s applies to %s, which the engine never builds as a resource", uid, typ)
				continue
			}
			req := cedartypes.Request{
				Principal: userUID(fixturePrincipal(r)),
				Action:    uid,
				Resource:  e.resourceUID(r, fixtureSlug),
				Context:   ctx,
			}
			if err := v.Request(req); err != nil {
				t.Errorf("%s on %s: %v", uid, typ, err)
			}
		}
	}
}

func TestBuiltinPolicyValidatesAgainstTheSchema(t *testing.T) {
	if err := policies.Validate("builtin", []byte(builtinPolicy)); err != nil {
		t.Error(err)
	}
}
