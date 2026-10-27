package middleware

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

// The themes the settings stub and its reader answer with.
const (
	themeBefore = "dark"  // what the reader reads before the update
	themeAfter  = "light" // what UpdateMine answers
)

// settingsWith records calls to UserSettingsService through an audit
// interceptor holding readers, and returns its client and the rows written.
func settingsWith(t *testing.T, readers StateReaders) (paladiniamv1connect.UserSettingsServiceClient, *recordingAuditWriter) {
	t.Helper()
	w := &recordingAuditWriter{}
	client := paladiniamv1connect.NewUserSettingsServiceClient(unarytest.Client(func(s *connect.Server) {
		paladiniamv1connect.RegisterUserSettingsServiceHandler(s, &stubSettings{})
	}, AuditWithState(w, "paladin-iam", false, nil, readers)))
	return client, w
}

// theme is the theme a state image holds, "" for none.
func theme(t *testing.T, image []byte) string {
	t.Helper()
	if image == nil {
		return ""
	}
	var s struct {
		Theme string `json:"theme"`
	}
	if err := json.Unmarshal(image, &s); err != nil {
		t.Fatalf("image %s: %v", image, err)
	}
	return s.Theme
}

func TestAuditRecordsTheStateAroundAnAction(t *testing.T) {
	ctx := principalCtx(uuid.New())

	t.Run("before from the reader, after from the answer when it is the resource", func(t *testing.T) {
		reads := 0
		client, w := settingsWith(t, StateReaders{
			paladiniamv1connect.UserSettingsServiceUpdateMineProcedure: func(context.Context, proto.Message) (proto.Message, error) {
				reads++
				return &iamv1.UserSettings{Theme: themeBefore}, nil
			},
		})
		if _, err := client.UpdateMine(ctx, &iamv1.UpdateMineRequest{}); err != nil {
			t.Fatal(err)
		}
		row := w.rows[0]
		if theme(t, row.StateBefore) != themeBefore || theme(t, row.StateAfter) != themeAfter {
			t.Errorf("before %s, after %s; want %q then %q", row.StateBefore, row.StateAfter, themeBefore, themeAfter)
		}
		if reads != 1 {
			t.Errorf("read %d times, want once: the answer is the resource", reads)
		}
	})

	// A delete answers with no resource, and the resource is gone after.
	t.Run("after read again when the answer is not the resource", func(t *testing.T) {
		reads := 0
		client, w := settingsWith(t, StateReaders{
			paladiniamv1connect.UserSettingsServiceDeleteForUserProcedure: func(context.Context, proto.Message) (proto.Message, error) {
				reads++
				if reads > 1 {
					return nil, connect.NewError(connect.CodeNotFound, "gone")
				}
				return &iamv1.UserSettings{Theme: themeBefore}, nil
			},
		})
		if _, err := client.DeleteForUser(ctx, &iamv1.DeleteForUserRequest{}); err != nil {
			t.Fatal(err)
		}
		row := w.rows[0]
		if theme(t, row.StateBefore) != themeBefore || row.StateAfter != nil {
			t.Errorf("before %s, after %s; want %q then none", row.StateBefore, row.StateAfter, themeBefore)
		}
		if reads != 2 {
			t.Errorf("read %d times, want twice", reads)
		}
	})

	t.Run("a failed read leaves its side empty and the action untouched", func(t *testing.T) {
		client, w := settingsWith(t, StateReaders{
			paladiniamv1connect.UserSettingsServiceUpdateMineProcedure: func(context.Context, proto.Message) (proto.Message, error) {
				return nil, connect.NewError(connect.CodeUnavailable, "down")
			},
		})
		if _, err := client.UpdateMine(ctx, &iamv1.UpdateMineRequest{}); err != nil {
			t.Fatalf("a failed read failed the action: %v", err)
		}
		if row := w.rows[0]; row.StateBefore != nil || row.StateAfter != nil {
			t.Errorf("before %s, after %s; want neither", row.StateBefore, row.StateAfter)
		}
	})

	// A capability narrowed to the change, without the read: the entry
	// goes without the image, and the log is not told of a fault.
	t.Run("a read the caller may not make leaves its side empty, unlogged", func(t *testing.T) {
		core, logs := observer.New(zap.WarnLevel)
		// The server reads its logger from its own context, which the
		// client's does not reach: the global one is what it falls back to.
		t.Cleanup(zap.ReplaceGlobals(zap.New(core)))
		client, w := settingsWith(t, StateReaders{
			paladiniamv1connect.UserSettingsServiceUpdateMineProcedure: func(context.Context, proto.Message) (proto.Message, error) {
				return nil, connect.NewError(connect.CodePermissionDenied, "op get not allowed")
			},
		})
		if _, err := client.UpdateMine(ctx, &iamv1.UpdateMineRequest{}); err != nil {
			t.Fatalf("a refused read failed the action: %v", err)
		}
		if row := w.rows[0]; row.StateBefore != nil || row.StateAfter != nil {
			t.Errorf("before %s, after %s; want neither", row.StateBefore, row.StateAfter)
		}
		if logs.Len() != 0 {
			t.Errorf("%d warnings for a read the caller may not make", logs.Len())
		}
	})

	t.Run("an action without a reader records no state", func(t *testing.T) {
		client, w := settingsWith(t, nil)
		if _, err := client.UpdateMine(ctx, &iamv1.UpdateMineRequest{}); err != nil {
			t.Fatal(err)
		}
		if row := w.rows[0]; row.StateBefore != nil || row.StateAfter != nil {
			t.Errorf("before %s, after %s; want neither", row.StateBefore, row.StateAfter)
		}
	})

	t.Run("a create records its answer after, and nothing before", func(t *testing.T) {
		client, w := settingsWith(t, StateReaders{paladiniamv1connect.UserSettingsServiceUpdateMineProcedure: nil})
		if _, err := client.UpdateMine(ctx, &iamv1.UpdateMineRequest{}); err != nil {
			t.Fatal(err)
		}
		if row := w.rows[0]; row.StateBefore != nil || theme(t, row.StateAfter) != themeAfter {
			t.Errorf("before %s, after %s; want none then %q", row.StateBefore, row.StateAfter, themeAfter)
		}
	})

	t.Run("a read is not made for an action the log skips", func(t *testing.T) {
		client, w := settingsWith(t, StateReaders{
			paladiniamv1connect.UserSettingsServiceGetMineProcedure: func(context.Context, proto.Message) (proto.Message, error) {
				t.Error("a read for a skipped action")
				return nil, nil
			},
		})
		if _, err := client.GetMine(ctx, &iamv1.GetMineRequest{}); err != nil {
			t.Fatal(err)
		}
		if len(w.rows) != 0 {
			t.Errorf("%d rows for a read", len(w.rows))
		}
	})
}

// The image is the whole resource, where a request carries only what it
// changes: a secret the request never named would land in the log forever.
// Every field the contract marks debug_redact is cleared from it.
func TestAuditStateIsRedacted(t *testing.T) {
	const (
		password = "kafka-password-not-for-the-log"
		key      = "tls-key-not-for-the-log"
	)
	sub := &adminv1.EventSubscription{Sink: &adminv1.EventSink{Target: &adminv1.EventSink_Kafka{Kafka: &adminv1.KafkaSink{
		Brokers: "kafka:9092", SaslPassword: password, TlsClientKey: key,
	}}}}
	client, w := settingsWith(t, StateReaders{
		paladiniamv1connect.UserSettingsServiceUpdateMineProcedure: func(context.Context, proto.Message) (proto.Message, error) {
			return sub, nil
		},
	})
	if _, err := client.UpdateMine(principalCtx(uuid.New()), &iamv1.UpdateMineRequest{}); err != nil {
		t.Fatal(err)
	}
	for side, image := range map[string][]byte{"before": w.rows[0].StateBefore, "after": w.rows[0].StateAfter} {
		if !strings.Contains(string(image), "kafka:9092") {
			t.Errorf("%s holds no sink: %s", side, image)
		}
		if strings.Contains(string(image), password) || strings.Contains(string(image), key) {
			t.Errorf("%s holds a secret: %s", side, image)
		}
	}
}
