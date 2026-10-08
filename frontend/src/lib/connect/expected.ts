import {
  createContextKey,
  createContextValues,
  type CallOptions,
} from "@connectrpc/connect";

// Some reads answer "nothing here yet" with NotFound — a capability or API
// token never used, a tenant with no budget or quota row. The caller turns
// that into a state, so the transport must not report it as an RPC error.
// The caller says so per call; NotFound from any other call is still logged.
export const kNotFoundIsAnswer = createContextKey(false, {
  description: "NotFound is an expected answer to this call",
});

// Call options for a read whose NotFound the caller handles as a state.
export function notFoundIsAnswer(opts: CallOptions = {}): CallOptions {
  const contextValues = opts.contextValues ?? createContextValues();
  contextValues.set(kNotFoundIsAnswer, true);
  return { ...opts, contextValues };
}
