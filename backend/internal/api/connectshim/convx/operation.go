package convx

import (
	"google.golang.org/genproto/googleapis/rpc/code"
	rpcstatus "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
)

// JSONToAny wraps an executor's JSON payload in an Any the wire can carry.
//
// operations.metadata / .response hold JSON produced by json.Marshal in the
// worker, not a serialized proto. Putting those bytes straight into
// &anypb.Any{Value: ...} with no TypeUrl gives proto nothing to marshal:
// "google.protobuf.Any: type_url is not set", and every ListOperations and
// GetOperation carrying a payload fails with CodeInternal.
//
// Decoding into a Struct keeps the payload readable to any client instead of
// making it an opaque blob, and gives the Any a real type_url. A payload that
// is not a JSON object (nothing writes one today) is dropped rather than
// failing the call: a listing is more useful without one row's metadata than
// not at all.
func JSONToAny(raw []byte) *anypb.Any {
	if len(raw) == 0 {
		return nil
	}
	var st structpb.Struct
	if err := protojson.Unmarshal(raw, &st); err != nil {
		return nil
	}
	packed, err := anypb.New(&st)
	if err != nil {
		return nil
	}
	return packed
}

// OperationError builds the `error` arm of Operation.result for a row that
// did not succeed.
//
// Both planes used to put every payload — failures included — into the
// `response` arm, leaving `error` permanently unset. The stored error_code and
// error_message reached no client at all, and a caller (the console's ops
// drawer among them) that asked `result.case == "error"` to tell a failure
// from a success got "success" for every failed operation.
//
// The stored response rides along in details, so a caller that reads the
// payload keeps reading it — for a reclaimed operation that payload carries
// last_progress, which is the only record of how far the work got.
func OperationError(errCode, errMsg string, response []byte, cancelled bool) *rpcstatus.Status {
	st := &rpcstatus.Status{
		Code:    int32(operationCode(errCode, cancelled)),
		Message: errMsg,
	}
	if st.Message == "" {
		st.Message = errCode
	}
	if payload := JSONToAny(response); payload != nil {
		st.Details = append(st.Details, payload)
	}
	return st
}

// operationCode maps the worker's domain error codes onto canonical ones.
//
// The domain code stays authoritative and travels in the details payload;
// this is the coarse classification a generic client can branch on.
func operationCode(errCode string, cancelled bool) code.Code {
	if cancelled {
		return code.Code_CANCELLED
	}
	switch errCode {
	case "WORKER_LOST":
		// The work may have been partially applied and nothing here can say
		// which half — ABORTED is the canonical "retry at a higher level".
		return code.Code_ABORTED
	case "UNKNOWN_TYPE":
		return code.Code_UNIMPLEMENTED
	default:
		return code.Code_UNKNOWN
	}
}
