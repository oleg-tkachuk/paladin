package admin

import (
	"fmt"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

// requireCompilablePolicy refuses Cedar text the engine cannot parse, at the
// edge where it enters the system.
//
// cedar.Validate has existed since the Validate RPC was written, and its own
// doc says it is "exposed for pre-save UI validation" — which left a button in
// a browser as the only thing between an operator and an entity whose
// authorization does not compile. That was not a guard. The console's Save
// posts whatever is in the textarea, every write path stored it unread, and
// the row then could not be read OR deleted: GetCollection, DeleteCollection
// and every authorization decision all route through the engine that cannot
// parse it, so each answers Internal. One keystroke wedged a collection out of
// the API permanently, and its bucket with it through the FK. Reproduced
// 2026-08-27 by the first e2e test ever pointed at this page.
//
// InvalidArgument, not Internal: unparseable policy text is the caller's
// mistake, and the parser's message is the useful half of the answer.
//
// Empty always passes — that is how a policy is cleared, not a policy that
// fails to compile.
func requireCompilablePolicy(text string) error {
	if text == "" {
		return nil
	}
	if err := cedar.Validate(text); err != nil {
		return connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("cedar policy does not compile: %w", err))
	}
	return nil
}
