// Package presignttl decides how long a presigned URL lives.
//
// A presigned URL is a bearer credential that bypasses Paladin and cannot be
// revoked before it expires, so its lifetime is policy, not a convenience.
// Every RPC that mints one — UploadObject, DownloadObject, PresignService,
// MultipartUploadService.PresignPart — resolves its TTL here, so one rule
// holds everywhere: an absent TTL takes the operation's configured default,
// and a TTL above limits.presign.max_ttl is refused rather than shortened.
package presignttl

import (
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

// Op names the kind of URL being minted; each has its own default lifetime.
type Op int

const (
	OpGet Op = iota + 1
	OpPut
	OpPart
)

func (o Op) String() string {
	switch o {
	case OpGet:
		return "get"
	case OpPut:
		return "put"
	case OpPart:
		return "part"
	}
	return fmt.Sprintf("op(%d)", int(o))
}

// Policy holds the per-operation defaults and the ceiling. Build it with New
// or FromConfig; the zero value refuses every request.
type Policy struct {
	get, put, part time.Duration
	max            time.Duration
}

// New validates and builds a Policy. Every default must be positive and no
// larger than max; max is bounded by config.SigV4MaxPresignExpiry.
func New(get, put, part, maxTTL time.Duration) (Policy, error) {
	if err := config.ValidatePresignTTLs(get, put, part, maxTTL); err != nil {
		return Policy{}, err
	}
	return Policy{get: get, put: put, part: part, max: maxTTL}, nil
}

// FromConfig builds the Policy from limits.presign. Config.Validate has
// already checked the same invariants at load, so an error here means the
// config never went through Load.
func FromConfig(c config.Presign) (Policy, error) {
	return New(c.GetTTL, c.PutTTL, c.PartTTL, c.MaxTTL)
}

// MustFromConfig is FromConfig for wiring, where a loaded config cannot fail it.
func MustFromConfig(c config.Presign) Policy {
	p, err := FromConfig(c)
	if err != nil {
		panic(fmt.Sprintf("presignttl: %v", err))
	}
	return p
}

// Max is the ceiling a caller-supplied TTL may not exceed.
func (p Policy) Max() time.Duration { return p.max }

// Default is the lifetime an operation gets when the caller names none.
func (p Policy) Default(op Op) time.Duration {
	switch op {
	case OpGet:
		return p.get
	case OpPut:
		return p.put
	case OpPart:
		return p.part
	}
	return 0
}

// ErrNotConfigured is returned by a zero Policy: a handler wired without one
// must refuse to mint rather than mint with no bound.
var ErrNotConfigured = errors.New("presign ttl policy is not configured")

// Resolve returns the TTL to sign with. requested == 0 means the caller sent
// none (an absent google.protobuf.Duration decodes to zero) and takes the
// operation's default. A negative TTL, or one above Max, is InvalidArgument:
// the contract says so, and silently shortening a requested lifetime leaves a
// caller holding a URL that dies earlier than it planned for.
func (p Policy) Resolve(op Op, requested time.Duration) (time.Duration, error) {
	return p.ResolveWithin(op, requested, 0)
}

// ResolveWithin is Resolve under a further ceiling — a bucket's
// max_presign_put_ttl or max_presign_get_ttl. A ceiling of zero or less sets
// none. The default shrinks to the ceiling when it is the smaller, so a
// caller who names no TTL is never refused for a limit it did not pick.
func (p Policy) ResolveWithin(op Op, requested, ceiling time.Duration) (time.Duration, error) {
	def := p.Default(op)
	if def <= 0 || p.max <= 0 {
		return 0, connect.NewError(connect.CodeInternal, ErrNotConfigured.Error()).WithCause(ErrNotConfigured)
	}
	limit, limitName := p.max, "limits.presign.max_ttl"
	if ceiling > 0 && ceiling < limit {
		limit, limitName = ceiling, "the bucket's presign ttl ceiling"
	}
	switch {
	case requested == 0:
		return min(def, limit), nil
	case requested < 0:
		return 0, connect.Errorf(connect.CodeInvalidArgument,
			"ttl %s must be positive", requested)
	case requested > limit:
		return 0, connect.Errorf(connect.CodeInvalidArgument,
			"ttl %s exceeds %s %s", requested, limitName, limit)
	}
	return requested, nil
}
