package multiparth

import (
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
)

// Every per-session multipart RPC carries an object_name AND an upload_id, and
// until assertSessionMatches existed the name was accepted unread: the session
// was located by id alone, so a request naming a different object was answered
// as though it named the right one. The pair disagreeing is a client that has
// lost track of which upload belongs to what, and it should hear about it.
func TestAssertSessionMatches(t *testing.T) {
	t.Parallel()

	objID := uuid.Must(uuid.NewV7())
	other := uuid.Must(uuid.NewV7())
	sess := Session{UploadID: "up-1", ObjectID: objID, Collection: "docs", Key: "a.pdf"}

	tests := []struct {
		name    string
		want    SessionRef
		wantErr string
	}{
		{
			name: "matching object and collection",
			want: SessionRef{Collection: "docs", ObjectID: objID},
		},
		{
			name: "empty ref makes no claim",
			want: SessionRef{},
		},
		{
			name:    "wrong object id",
			want:    SessionRef{Collection: "docs", ObjectID: other},
			wantErr: "belongs to " + objID.String(),
		},
		{
			name:    "wrong collection",
			want:    SessionRef{Collection: "photos", ObjectID: objID},
			wantErr: `belongs to "docs"`,
		},
		{
			name: "collection-only claim that matches",
			want: SessionRef{Collection: "docs"},
		},
		{
			name:    "collection-only claim that does not",
			want:    SessionRef{Collection: "photos"},
			wantErr: "collection",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := assertSessionMatches(sess, tc.want)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("assertSessionMatches: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("mismatched object_name was accepted")
			}
			if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
				t.Errorf("code = %v, want InvalidArgument", got)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}
