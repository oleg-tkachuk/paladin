package health

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/grpchealth/v2"
)

const testService = "paladin.iam.v1.HealthService"

var errDown = errors.New("down")

func failing(critical bool) Check {
	return Check{Name: "dep", Critical: critical, Func: func(context.Context) error { return errDown }}
}

// The gRPC health protocol answers as /readyz does.
func TestGRPCCheckerAnswersAsReadyz(t *testing.T) {
	for _, tc := range []struct {
		name     string
		ready    []Probe
		draining bool
		want     grpchealth.Status
	}{
		{"healthy", nil, false, grpchealth.StatusServing},
		{"a non-critical check fails: degraded, still serving", []Probe{failing(false)}, false, grpchealth.StatusServing},
		{"a critical check fails", []Probe{failing(true)}, false, grpchealth.StatusNotServing},
		{"draining", nil, true, grpchealth.StatusNotServing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{Ready: tc.ready}
			if tc.draining {
				h.MarkShuttingDown()
			}
			for _, service := range []string{"", testService} {
				resp, err := h.GRPCChecker(testService).Check(context.Background(), &grpchealth.CheckRequest{Service: service})
				if err != nil {
					t.Fatalf("service %q: %v", service, err)
				}
				if resp.Status != tc.want {
					t.Fatalf("service %q: status = %v, want %v", service, resp.Status, tc.want)
				}
			}
		})
	}
}

// The protocol requires NotFound for a service the server does not serve.
func TestGRPCCheckerRefusesAnUnknownService(t *testing.T) {
	h := &Handler{}
	_, err := h.GRPCChecker(testService).Check(context.Background(), &grpchealth.CheckRequest{Service: "acme.Other"})
	if got := connect.CodeOf(err); got != connect.CodeNotFound {
		t.Fatalf("code = %v (%v), want not_found", got, err)
	}
}
