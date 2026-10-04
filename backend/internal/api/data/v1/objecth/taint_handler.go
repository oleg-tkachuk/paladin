package objecth

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/capability"
)

// Taint signals an object's content can be flagged with. The closed set the
// proto enum TaintSignal names; stored in objects.taint as these strings.
const (
	TaintPromptInjection = "prompt_injection"
	TaintPII             = "pii"
	TaintSecrets         = "secrets"
)

var knownTaintSignals = []string{TaintPromptInjection, TaintPII, TaintSecrets}

// ErrObjectNotFound is the TaintRepository's answer for an object the
// tenant does not have.
var ErrObjectNotFound = errors.New("object not found")

// ErrUnknownTaintSignal rejects a signal outside the closed set.
var ErrUnknownTaintSignal = errors.New("unknown taint signal")

// TaintRepository persists objects.taint.
type TaintRepository interface {
	// SetTaint replaces the object's signals and returns what was stored.
	SetTaint(ctx context.Context, tenantID, objectID uuid.UUID, signals []string) ([]string, error)
	// TaintAtPath returns the signals of the live object at a path, or none
	// when there is no live object there.
	TaintAtPath(ctx context.Context, tenantID uuid.UUID, collection, key string) ([]string, error)
}

// TaintHandler implements SetObjectTaint and the taint lookup the
// capability read gate uses. A sibling of Handler, like LockHandler, so the
// main handler's dependencies do not grow for a feature most calls never
// touch.
type TaintHandler struct {
	objects Repository
	taints  TaintRepository
	policy  cedar.Authorizer
}

// NewTaintHandler builds the handler.
func NewTaintHandler(objects Repository, taints TaintRepository, policy cedar.Authorizer) *TaintHandler {
	return &TaintHandler{objects: objects, taints: taints, policy: policy}
}

// SetTaint replaces an object's taint signals; an empty list clears them.
// Gated by its own Cedar action, and for a capability by OpManage on the
// object: clearing a flag re-opens content to agents.
func (h *TaintHandler) SetTaint(ctx context.Context, collection, objectID string, signals []string) (*Object, error) {
	signals, err := normaliseTaint(signals)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	tenantID, principal, err := apiutil.ActingContext(ctx)
	if err != nil {
		return nil, err
	}
	if collection == "" || objectID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("collection and object_id are required"))
	}
	obj, err := h.objects.FindByName(ctx, tenantID, collection, objectID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpManage,
		CapabilityObjectURI(tenantID, obj.Collection, obj.Key)); err != nil {
		return nil, err
	}
	if h.policy != nil {
		decision, err := h.policy.IsAuthorized(ctx,
			apiutil.CedarPrincipal(principal),
			cedar.ActionSetObjectTaint,
			&cedar.Resource{
				TenantID:    tenantID,
				Collection:  obj.Collection,
				Key:         obj.Key,
				ObjectID:    obj.ObjectID,
				State:       string(obj.State),
				SizeBytes:   obj.SizeBytes,
				ContentType: obj.ContentType,
				Tags:        obj.Tags,
			},
			cedar.RequestContext{SizeBytes: obj.SizeBytes, ContentType: obj.ContentType, Now: time.Now()},
		)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if decision != cedar.DecisionAllow {
			return nil, connect.NewError(connect.CodePermissionDenied,
				errors.New("not authorised to set taint on this object"))
		}
	}
	stored, err := h.taints.SetTaint(ctx, tenantID, obj.ObjectID, signals)
	if errors.Is(err, ErrObjectNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	obj.Taint = stored
	return &obj, nil
}

// Tainted reports whether the object a capability URI names carries any
// taint signal. It is the lookup the capability read gate calls; a URI that
// is not an object URI, or names no live object, is not tainted — the
// handler that serves the read reports the missing object itself.
func (h *TaintHandler) Tainted(ctx context.Context, uri string) (bool, error) {
	tenantID, collection, key, ok := parseCapabilityObjectURI(uri)
	if !ok {
		return false, nil
	}
	signals, err := h.taints.TaintAtPath(ctx, tenantID, collection, key)
	if err != nil {
		return false, fmt.Errorf("taint lookup: %w", err)
	}
	return len(signals) > 0, nil
}

// normaliseTaint validates against the closed set, drops duplicates and
// sorts, so the stored array has one spelling per set of signals.
func normaliseTaint(signals []string) ([]string, error) {
	out := make([]string, 0, len(signals))
	for _, s := range signals {
		if !slices.Contains(knownTaintSignals, s) {
			return nil, fmt.Errorf("%w: %q (known: %v)", ErrUnknownTaintSignal, s, knownTaintSignals)
		}
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return out, nil
}
