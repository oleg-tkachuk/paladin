package batchh

import (
	"encoding/json"
	"reflect"
	"testing"
	"uuid"
)

func TestDescribeOperation(t *testing.T) {
	tenant := uuid.New()
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	coll := CollectionName(tenant, "e2e/docs")
	marshal := func(v any) []byte {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	cases := []struct {
		name   string
		opType string
		args   any
		want   Target
	}{
		{"a delete to the trash", OpBatchDelete,
			BatchDeleteArgs{TenantID: tenant, Collection: "e2e/docs", ObjectIDs: ids},
			Target{Collection: coll, ObjectCount: 3}},
		{"a permanent delete", OpBatchDelete,
			BatchDeleteArgs{TenantID: tenant, Collection: "e2e/docs", ObjectIDs: ids[:1], Permanent: true},
			Target{Collection: coll, ObjectCount: 1, Permanent: true}},
		{"a copy names both collections", OpBatchCopy,
			BatchCopyArgs{TenantID: tenant, SrcCollection: "e2e/docs", DstCollection: "e2e/archive", ObjectIDs: ids},
			Target{Collection: coll, Destination: CollectionName(tenant, "e2e/archive"), ObjectCount: 3}},
		// A retry of a copy asks for the same keys again.
		{"a copy names its key prefix", OpBatchCopy,
			BatchCopyArgs{TenantID: tenant, SrcCollection: "e2e/docs", DstCollection: "e2e/archive", ObjectIDs: ids, KeyPrefix: "2026/"},
			Target{Collection: coll, Destination: CollectionName(tenant, "e2e/archive"), ObjectCount: 3, KeyPrefix: "2026/"}},
		{"a tag update that replaces", OpBatchUpdateTags,
			BatchUpdateTagsArgs{TenantID: tenant, Collection: "e2e/docs", ObjectIDs: ids[:2], Tags: map[string]string{"a": "b"}, Replace: true},
			Target{Collection: coll, ObjectCount: 2, Replace: true, Tags: map[string]string{"a": "b"}}},
		{"a restore", OpBatchRestoreObjects,
			BatchRestoreObjectsArgs{TenantID: tenant, Collection: "e2e/docs", ObjectIDs: ids},
			Target{Collection: coll, ObjectCount: 3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := DescribeOperation(tc.opType, marshal(tc.args))
			if !ok || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("DescribeOperation = %+v, %v; want %+v", got, ok, tc.want)
			}
		})
	}
}

// An operation the listing cannot describe is listed without a target, never
// as an error.
func TestDescribeOperation_UnknownOrUnreadable(t *testing.T) {
	for name, tc := range map[string]struct {
		opType string
		md     []byte
	}{
		"an unknown type":       {"Reindex", []byte(`{}`)},
		"arguments not in JSON": {OpBatchDelete, []byte("not json")},
		"no arguments":          {OpBatchCopy, nil},
	} {
		if _, ok := DescribeOperation(tc.opType, tc.md); ok {
			t.Errorf("%s: described", name)
		}
	}
}
