package app

import (
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectproto"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/codec"
)

// Size limits on one RPC message. Object bytes never travel in an RPC — they
// go to storage over presigned URLs — so a message is metadata: names, tags,
// a policy, a page of a listing. The limits bound what one caller can make a
// replica hold. They are the Go SDK's, which reads responses up to the same
// size the server sends, so the two cannot drift apart.
const (
	// MaxRPCRequestBytes is the largest request the planes read, gRPC's own
	// default. A batch RPC is capped by count well below it.
	MaxRPCRequestBytes = paladin.MaxRequestBytes
	// MaxRPCResponseBytes is the largest response the planes send. Higher than
	// a request: a full page of a listing with tags is the largest message the
	// contract has, and a page that could not be sent would fail the listing.
	MaxRPCResponseBytes = paladin.MaxResponseBytes
	// RPCCompressMinBytes is the smallest response worth compressing; below it
	// gzip's framing costs more than it saves.
	RPCCompressMinBytes = 1 << 10
)

// rpcMountOptions are the HTTP options every plane's server is mounted with:
// the strict JSON codec, the size limits, and compression only where it pays.
func rpcMountOptions() []connecthttp.Option {
	return []connecthttp.Option{
		// WithCodecs replaces the defaults, so binary protobuf is listed too.
		// An unknown JSON request field is a 400, not a silent discard: see
		// internal/api/codec for why the forward-compatibility the default
		// buys is not worth its cost here.
		connecthttp.WithCodecs(connectproto.NewBinaryCodec(), codec.StrictJSON{}),
		connecthttp.WithReadMaxBytes(MaxRPCRequestBytes),
		connecthttp.WithSendMaxBytes(MaxRPCResponseBytes),
		connecthttp.WithCompressMinBytes(RPCCompressMinBytes),
	}
}
