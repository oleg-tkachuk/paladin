package celh

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/tests/fuzzassert"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// validateCodes is what Validate answers a request with: an unknown schema.
// An expression that does not compile is an answer, not an error.
var validateCodes = []connect.Code{connect.CodeInvalidArgument}

// Whatever schema and expression an operator types, Validate refuses only an
// unknown schema, and the diagnostic it returns for a bad expression is text
// the console can show as it is: printable, and naming nothing of the server.
func FuzzValidate(f *testing.F) {
	for _, c := range [][2]string{
		{"Object", ""},
		{"EventEnvelope", `type == "paladin.object.uploaded"`},
		{"Object", `nonexistent_field == "x"`},
		{"Object", `state == "available"`},
		{"Object", "state == \"a\x07\""},
		{"Object", `size_bytes > 1024 && key.startsWith("a/")`},
		{"Collection", `name.matches("^a")`},
		{"AuditLogEntry", `(`},
		{"BogusSchema", "true"},
		{"Nope", ""},
	} {
		f.Add(c[0], c[1])
	}
	h := NewHandler()
	f.Fuzz(func(t *testing.T, schema, expression string) {
		resp, err := h.Validate(context.Background(), &pb.ValidateCELRequest{Schema: schema, Expression: expression})
		fuzzassert.Refusal(t, err, validateCodes, schema, expression)
		if err != nil {
			return
		}
		if !resp.GetValid() {
			fuzzassert.Text(t, resp.GetMessage(), expression)
		}
	})
}
