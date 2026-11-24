package iam

import (
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
)

// unknownSlug is a tenant slug countingSlugs does not resolve.
const unknownSlug = "nobody"

// CreateUser judges the request before it looks the parent's slug up: a
// request missing its subject or password is the caller's mistake whichever
// tenant it names, and answering NotFound first hid it until the slug was
// fixed.
func TestCreateUserValidatesTheRequestBeforeTheSlugLookup(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		subject, password string
		want              connect.Code
		wantLookups       int
	}{
		"no subject":         {password: "p", want: connect.CodeInvalidArgument},
		"no password":        {subject: "u", want: connect.CodeInvalidArgument},
		"a complete request": {subject: "u", password: "p", want: connect.CodeNotFound, wantLookups: 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			core, logs := observer.New(zapcore.DebugLevel)
			ctx := logger.WithContext(asCaller(uuid.New(), "platform", apiutil.RolePlatformAdmin), zap.New(core))
			slugs := &countingSlugs{slug: "globex", id: uuid.New()}
			h := &listingUser{}
			_, err := (&UserServer{H: h, tenants: slugs}).CreateUser(ctx, &pb.CreateUserRequest{
				Parent: "tenants/" + unknownSlug, Subject: tc.subject, InitialPassword: tc.password,
			})
			if got := connect.CodeOf(err); got != tc.want {
				t.Fatalf("code = %v, want %v (err: %v)", got, tc.want, err)
			}
			if slugs.lookups != tc.wantLookups {
				t.Errorf("%d slug lookup(s), want %d", slugs.lookups, tc.wantLookups)
			}
			if tc.want != connect.CodeInvalidArgument {
				return
			}
			refused := logs.FilterLevelExact(zapcore.WarnLevel).
				FilterField(zap.String(logOp, opCreateUser)).All()
			if len(refused) != 1 {
				t.Errorf("refusal logged %d times at Warn, want once: %v", len(refused), logs.All())
			}
		})
	}
}
