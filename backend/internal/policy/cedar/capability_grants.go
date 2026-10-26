package cedar

// GrantBasis says where what a capability operation unlocks comes from.
type GrantBasis int

const (
	// GrantBuiltin: the built-in policy grants Actions in the capability's
	// own tenant.
	GrantBuiltin GrantBasis = iota + 1
	// GrantTenantPolicy: nothing without a tenant policy naming the actions.
	GrantTenantPolicy
	// GrantPaired: asserted only beside PairedOps, whose rows grant the
	// action; the operation adds a requirement, never an action.
	GrantPaired
	// GrantCaveatsOnly: the caveats alone decide and no policy is asked;
	// Procedures are the RPCs they open.
	GrantCaveatsOnly
)

// CapabilityOperationGrant is what one capability operation unlocks with no
// tenant policy (ADR-0038).
type CapabilityOperationGrant struct {
	// Op is the operation as caveats.ops names it.
	Op    string
	Basis GrantBasis
	// Actions are granted to a capability whose request admitted Op, for
	// GrantBuiltin.
	Actions []Action
	// PairedOps are the operations Op is asserted beside, for GrantPaired.
	PairedOps []string
	// Procedures are the RPCs the caveats alone open, as service.Method,
	// for GrantCaveatsOnly.
	Procedures []string
}

// Capability operations as caveats name them: limes' shared ones (limes.OpGet
// …) and the data plane's own (auth.OpPresign, auth.OpTag). Spelled out
// because the built-in policy compares context.capability_op to these exact
// strings; capability_grants_test.go holds them to limes' and the SDK's.
const (
	CapabilityOpGet     = "get"
	CapabilityOpList    = "list"
	CapabilityOpPut     = "put"
	CapabilityOpDelete  = "delete"
	CapabilityOpShare   = "share"
	CapabilityOpManage  = "manage"
	CapabilityOpPresign = "object:presign"
	CapabilityOpTag     = "object:tag"
)

// procedureDelegate is the RPC an OpShare capability opens.
const procedureDelegate = "paladin.admin.v1.CapabilityService.Delegate"

// CapabilityOperationGrants is the table builtinPolicy implements for
// capabilities, one row per operation, in the order the console lists them.
// The policy text is the authority Cedar evaluates; this table is what the
// API serves about it, and TestCapabilityOperationGrants_MatchTheBuiltin
// fails when the two disagree.
//
// RestoreObject and DeleteObject come from the machine delete permit, which
// does not read the operation: the row is the one whose handler asserts it
// (RestoreObject asserts put, DeleteObject delete).
var CapabilityOperationGrants = []CapabilityOperationGrant{
	{Op: CapabilityOpGet, Basis: GrantBuiltin, Actions: []Action{ActionGetObject, ActionPresignGet}},
	{Op: CapabilityOpPut, Basis: GrantBuiltin, Actions: []Action{
		ActionPutObject, ActionPresignPut, ActionUpdateObject, ActionCopyObject, ActionRestoreObject,
	}},
	{Op: CapabilityOpList, Basis: GrantBuiltin, Actions: []Action{ActionGetObject, ActionPresignGet}},
	{Op: CapabilityOpDelete, Basis: GrantBuiltin, Actions: []Action{ActionDeleteObject}},
	{Op: CapabilityOpShare, Basis: GrantCaveatsOnly, Procedures: []string{procedureDelegate}},
	{Op: CapabilityOpManage, Basis: GrantTenantPolicy},
	{Op: CapabilityOpPresign, Basis: GrantPaired, PairedOps: []string{CapabilityOpGet, CapabilityOpPut}},
	{Op: CapabilityOpTag, Basis: GrantBuiltin, Actions: []Action{ActionUpdateObject}},
}

// machineDeleteActions are the actions the machine delete permit grants every
// capability in its tenant, whatever operation the request admitted.
var machineDeleteActions = []Action{ActionDeleteObject, ActionRestoreObject}
