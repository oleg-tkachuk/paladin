package cedar

// CapabilityOperationGrant is what one capability operation unlocks in the
// capability's own tenant with no tenant policy (ADR-0038): the caveats admit
// the operation in the handler, and the built-in policy grants these actions.
type CapabilityOperationGrant struct {
	// Op is the operation as caveats.ops names it.
	Op string
	// Actions are granted to a capability whose request admitted Op. Empty
	// when every action of the operation needs a tenant policy naming it.
	Actions []Action
}

// Capability operations, as limes spells them (limes.OpGet …). Spelled out
// here because the built-in policy compares context.capability_op to these
// exact strings; capability_grants_test.go holds them to limes'.
const (
	CapabilityOpGet    = "get"
	CapabilityOpList   = "list"
	CapabilityOpPut    = "put"
	CapabilityOpDelete = "delete"
	CapabilityOpManage = "manage"
)

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
	{Op: CapabilityOpGet, Actions: []Action{ActionGetObject, ActionPresignGet}},
	{Op: CapabilityOpList, Actions: []Action{ActionGetObject, ActionPresignGet}},
	{Op: CapabilityOpPut, Actions: []Action{
		ActionPutObject, ActionPresignPut, ActionUpdateObject, ActionCopyObject, ActionRestoreObject,
	}},
	{Op: CapabilityOpDelete, Actions: []Action{ActionDeleteObject}},
	{Op: CapabilityOpManage},
}

// machineDeleteActions are the actions the machine delete permit grants every
// capability in its tenant, whatever operation the request admitted.
var machineDeleteActions = []Action{ActionDeleteObject, ActionRestoreObject}
