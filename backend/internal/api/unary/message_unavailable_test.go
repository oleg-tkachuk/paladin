package unary_test

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/tests/fuzzassert"
)

// unregisteredProcedure is a procedure of a service no binary registers.
const unregisteredProcedure = "/unregistered.v1.Svc/Call"

// unregisteredMethod is the method of a file built here and never added to
// the global registry, so neither of its messages can be found there.
func unregisteredMethod(t *testing.T) connect.Spec {
	t.Helper()
	fd, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:    proto.String("unregistered/v1/svc.proto"),
		Package: proto.String("unregistered.v1"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{
			{Name: proto.String("CallRequest")}, {Name: proto.String("CallResponse")},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String("Svc"),
			Method: []*descriptorpb.MethodDescriptorProto{{
				Name:       proto.String("Call"),
				InputType:  proto.String(".unregistered.v1.CallRequest"),
				OutputType: proto.String(".unregistered.v1.CallResponse"),
			}},
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return connect.Spec{Procedure: unregisteredProcedure, Schema: fd.Services().Get(0).Methods().Get(0)}
}

// A message the server cannot build is answered with a fixed sentence, never
// the registry's error, which is logged at Error with the procedure.
func TestMessageUnavailableAnswersAFixedText(t *testing.T) {
	cases := map[string]connect.Spec{
		"no schema":            {Procedure: unregisteredProcedure},
		"an unregistered type": unregisteredMethod(t),
	}
	build := map[string]func(context.Context, connect.Spec) (proto.Message, error){
		"request":  unary.NewRequest,
		"response": unary.NewResponse,
	}
	for name, spec := range cases {
		for which, newMsg := range build {
			t.Run(name+"/"+which, func(t *testing.T) {
				core, logs := observer.New(zapcore.DebugLevel)
				ctx := logger.WithContext(context.Background(), zap.New(core))
				_, err := newMsg(ctx, spec)
				fuzzassert.Refusal(t, err, []connect.Code{connect.CodeInternal})
				ce, _ := errors.AsType[*connect.Error](err)
				if ce.Message() != unary.MessageUnavailable {
					t.Errorf("message = %q, want %q", ce.Message(), unary.MessageUnavailable)
				}
				entries := logs.FilterLevelExact(zapcore.ErrorLevel).All()
				if len(entries) != 1 {
					t.Fatalf("logged %d lines at error, want 1", len(entries))
				}
				fields := entries[0].ContextMap()
				if fields["rpc"] != unregisteredProcedure || fields["error"] == "" {
					t.Errorf("fields = %v, want the procedure and the cause", fields)
				}
			})
		}
	}
}
