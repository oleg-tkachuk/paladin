package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/codec"
)

// Size limits on one RPC message. Object bytes never travel in an RPC — they
// go to storage over presigned URLs — so a message is metadata: names, tags,
// a policy, a page of a listing. Without a limit connect reads a request of
// any size into memory; these bound what one caller can make a replica hold.
const (
	// MaxRPCRequestBytes is the largest request the planes read, gRPC's own
	// default. A batch RPC is capped by count well below it.
	MaxRPCRequestBytes = 4 << 20
	// MaxRPCResponseBytes is the largest response the planes send. Higher than
	// a request: a full page of a listing with tags is the largest message the
	// contract has, and a page that could not be sent would fail the listing.
	MaxRPCResponseBytes = 16 << 20
	// RPCCompressMinBytes is the smallest response worth compressing; below it
	// gzip's framing costs more than it saves.
	RPCCompressMinBytes = 1 << 10
)

// errPanicked is what a caller sees for a handler that panicked: the internal
// error, with nothing of the panic in it.
var errPanicked = errors.New("internal error")

// rpcHandlerOptions are the options every plane's handlers share: the strict
// JSON codec, the size limits, compression only where it pays, and a panic
// answered as an internal error and logged with its stack instead of dropping
// the connection.
func rpcHandlerOptions(l *zap.Logger) connect.HandlerOption {
	return connect.WithHandlerOptions(
		// An unknown request field is a 400, not a silent discard. See
		// internal/api/codec for why the forward-compatibility the default
		// buys is not worth its cost here.
		connect.WithCodec(codec.StrictJSON{}),
		connect.WithReadMaxBytes(MaxRPCRequestBytes),
		connect.WithSendMaxBytes(MaxRPCResponseBytes),
		connect.WithCompressMinBytes(RPCCompressMinBytes),
		connect.WithRecover(func(ctx context.Context, spec connect.Spec, _ http.Header, recovered any) error {
			l.Error("rpc handler panicked",
				zap.String("procedure", spec.Procedure),
				zap.String("panic", fmt.Sprint(recovered)),
				zap.StackSkip("stack", 1),
			)
			return connect.NewError(connect.CodeInternal, errPanicked)
		}),
	)
}
