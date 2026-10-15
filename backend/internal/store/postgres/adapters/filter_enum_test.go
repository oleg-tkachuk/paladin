package adapters

import (
	"sort"
	"strings"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// The values a filter may compare an enum-backed field with are the values of
// the SQL enum behind it. sqlc generates these constants from the migrations,
// so a value added to or dropped from an enum fails here instead of turning a
// valid filter into InvalidArgument, or letting an invalid one reach Postgres.
func TestFilterEnumsMatchSQLEnums(t *testing.T) {
	cases := []struct {
		schema *cel.Schema
		field  string
		sql    []string
	}{
		{cel.ObjectSchema, "state", []string{
			string(sqlc.ObjectStatePENDING), string(sqlc.ObjectStateAVAILABLE),
			string(sqlc.ObjectStateDELETED),
		}},
		{cel.OperationSchema, "state", []string{
			string(sqlc.OperationStatePENDING), string(sqlc.OperationStateRUNNING),
			string(sqlc.OperationStateSUCCEEDED), string(sqlc.OperationStateFAILED),
			string(sqlc.OperationStateCANCELLED),
		}},
		{cel.TenantSchema, "storage_layout", []string{
			string(sqlc.TenantStorageLayoutShared), string(sqlc.TenantStorageLayoutDedicated),
		}},
	}
	for _, tc := range cases {
		got, ok := tc.schema.EnumValues(tc.field)
		if !ok {
			t.Errorf("%s.%s is not enum-backed", tc.schema.Name, tc.field)
			continue
		}
		sort.Strings(tc.sql)
		if strings.Join(got, ",") != strings.Join(tc.sql, ",") {
			t.Errorf("%s.%s accepts %v, SQL enum holds %v", tc.schema.Name, tc.field, got, tc.sql)
		}
	}
}
