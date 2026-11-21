package objecth

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// Taint signals an object's content can be flagged with. The closed set the
// proto enum TaintSignal names; stored in objects.taint as these strings.
const (
	TaintPromptInjection = "prompt_injection"
	TaintPII             = "pii"
	TaintSecrets         = "secrets"
)

var knownTaintSignals = []string{TaintPromptInjection, TaintPII, TaintSecrets}

// ErrObjectNotFound is the repositories' answer for an object the tenant
// does not have.
var ErrObjectNotFound = errors.New("object not found")

// objectLookupError answers a failed object lookup. An object the tenant does
// not have is NotFound, in those words; anything else is the store failing,
// which is Internal. Mapping every error to NotFound handed the caller the
// driver's own text ("no rows in result set") and reported a database outage
// as a missing object.
func objectLookupError(err error) error {
	if errors.Is(err, ErrObjectNotFound) {
		return connect.NewError(connect.CodeNotFound, ErrObjectNotFound.Error()).WithCause(ErrObjectNotFound)
	}
	return connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
}

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
	log     *zap.Logger
}

// SetLogger sets where SetTaint says how it ended. Unset, nothing is logged.
func (h *TaintHandler) SetLogger(l *zap.Logger) {
	if h != nil { // the wiring builds none where its repository is not wired
		h.log = l
	}
}

// NewTaintHandler builds the handler.
func NewTaintHandler(objects Repository, taints TaintRepository, policy cedar.Authorizer) *TaintHandler {
	return &TaintHandler{objects: objects, taints: taints, policy: policy}
}

// SetTaint replaces an object's taint signals; an empty list clears them.
// Gated by its own Cedar action, and for a capability by OpManage on the
// object: clearing a flag re-opens content to agents.
func (h *TaintHandler) SetTaint(ctx context.Context, collection, objectID string, signals []string) (_ *Object, err error) {
	defer func() {
		logOutcome(ctx, h.log, opSetTaint, zapcore.InfoLevel, err,
			append(objectFields(collection, objectID), zap.Strings("signals", signals))...)
	}()
	signals, err = normaliseTaint(signals)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	tenantID, principal, err := apiutil.ActingContext(ctx)
	if err != nil {
		return nil, err
	}
	obj, err := h.target(ctx, tenantID, collection, objectID)
	if err != nil {
		return nil, err
	}
	if err := auth.AssertCapabilityOp(ctx, limes.OpManage,
		CapabilityObjectURI(tenantID, obj.Collection, obj.Key)); err != nil {
		return nil, err
	}
	// Setting a taint is a write on the object: it answers the bucket's state
	// as every other write does, and Cedar decides on the resolved binding.
	backendID, bucket, err := writeBinding(ctx, h.objects, tenantID, obj.Collection)
	if err != nil {
		return nil, err
	}
	if h.policy != nil {
		if err := apiutil.Decide(ctx, h.policy,
			apiutil.CedarPrincipal(principal),
			cedar.ActionSetObjectTaint,
			&cedar.Resource{
				TenantID:    tenantID,
				Collection:  obj.Collection,
				Key:         obj.Key,
				BackendID:   backendID,
				BucketName:  bucket,
				ObjectID:    obj.ObjectID,
				State:       string(obj.State),
				SizeBytes:   obj.SizeBytes,
				ContentType: obj.ContentType,
				Tags:        obj.Tags,
			},
			cedar.RequestContext{SizeBytes: obj.SizeBytes, ContentType: obj.ContentType},
		); err != nil {
			return nil, err
		}
	}
	stored, err := h.taints.SetTaint(ctx, tenantID, obj.ObjectID, signals)
	if err != nil {
		return nil, objectLookupError(err)
	}
	obj.Taint = stored
	return &obj, nil
}

// target is the object a SetTaint request names.
func (h *TaintHandler) target(ctx context.Context, tenantID uuid.UUID, collection, objectID string) (Object, error) {
	if err := requireObjectName(collection, objectID); err != nil {
		return Object{}, err
	}
	obj, err := h.objects.FindByName(ctx, tenantID, collection, objectID)
	if err != nil {
		return Object{}, objectLookupError(err)
	}
	return obj, nil
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
