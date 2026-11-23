package policyh

import (
	"context"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/tests/fuzzassert"
)

// permitLayers is a policy store whose tenant layer permits everything, so a
// simulation reaches the real engine with the request's principal.
type permitLayers struct{}

func (permitLayers) Fetch(context.Context, uuid.UUID, string) (cedar.Layers, []byte, string, error) {
	return cedar.Layers{Tenant: "permit(principal, action, resource);"}, nil, "", nil
}
func (permitLayers) Watch(context.Context) (<-chan cedar.ChangeEvent, error) { return nil, nil }

// engineTTL is how long the fuzzed engine caches a tenant's policies; the
// layers never change, so any value holds.
const engineTTL = time.Minute

// validateCodes: ValidatePolicy answers policy text it cannot read with a
// diagnostic, never an error, so no code is the caller's.
var validateCodes []connect.Code

// Whatever text an operator types, ValidatePolicy answers it with diagnostics
// the console can show as they are, and never fails on it.
func FuzzValidatePolicy(f *testing.F) {
	for _, text := range []string{
		"",
		"permit (\n  principal,\n  action,\n  resource\n);",
		`forbid(principal, action == Paladin::Action::"DeleteObject", resource) when { resource.key like "*.exe" };`,
		`permit(principal, action, resource) when { resource.nope == 1 };`,
		`permit(principal in Paladin::Role::"admin", action, resource);`,
		"permit(",
		"@id(\"\x00\") permit(principal, action, resource);",
	} {
		f.Add(text)
	}
	h := NewHandler(allowEngine(), &fakeStore{})
	ctx := authedCtx(uuid.New())
	f.Fuzz(func(t *testing.T, text string) {
		out, err := h.ValidatePolicy(ctx, text)
		fuzzassert.Refusal(t, err, validateCodes, text)
		if err != nil {
			return
		}
		for _, d := range out.Diagnostics {
			fuzzassert.Text(t, d.Message, text)
		}
	})
}

// simulateCodes is what SimulateAuthz answers a malformed question with: a
// resource name or an action it cannot read.
var simulateCodes = []connect.Code{connect.CodeInvalidArgument}

// Whatever principal, action and resource a simulation names, the real
// engine decides it or the handler refuses it as the caller's mistake, in
// words that name nothing of the server; the explanation it answers with is
// text the console can show as it is.
func FuzzSimulateAuthz(f *testing.F) {
	tid := uuid.New().String()
	for _, c := range [][5]string{
		{"u1", "admin", "user", cedar.ActionGetObject.String(), "tenants/" + tid},
		{"u1", "", "", cedar.ActionDeleteObject.String(), "tenants/" + tid + "/collections/e2e/logs"},
		{"u1", "", "service", cedar.ActionGetObject.String(), "tenants/" + tid + "/collections/c/objects/" + tid},
		{"", "", "", "issue", "tenants/not-a-uuid/collections/k"},
		{"a\"b", "x\x00", "agent", cedar.ActionGetObject.String(), "storageBackends/b/buckets/x"},
	} {
		f.Add(c[0], c[1], c[2], c[3], c[4])
	}
	h := NewHandler(cedar.NewEngine(permitLayers{}, engineTTL), &fakeStore{})
	f.Fuzz(func(t *testing.T, subject, role, kind, action, resource string) {
		in := SimulateAuthzInput{
			PrincipalSubject: subject, PrincipalTenantID: uuid.New(), PrincipalRoles: []string{role},
			PrincipalKind: kind, Action: action, ResourceName: resource,
		}
		out, err := h.SimulateAuthz(authedCtx(uuid.New()), in)
		fuzzassert.Refusal(t, err, simulateCodes, subject, role, kind, action, resource)
		if err == nil {
			fuzzassert.Text(t, out.Explanation, action, resource)
		}
	})
}
