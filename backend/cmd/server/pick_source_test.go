package main

import (
	"strings"
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/eventingest"
)

// TestPickSource covers the ingest source_format → adapter factory, including
// the S3 generalisation and the deliberate Garage rejection.
func TestPickSource(t *testing.T) {
	t.Run("s3 and minio both resolve to the S3 adapter", func(t *testing.T) {
		for _, f := range []string{"s3", "minio"} {
			src, err := pickSource(f)
			if err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			if _, ok := src.(*eventingest.S3EventSource); !ok {
				t.Errorf("%s: got %T, want *S3EventSource", f, src)
			}
			// The label is the source_format so metrics/logs attribute it.
			if src.Name() != f {
				t.Errorf("%s: Name() = %q, want %q", f, src.Name(), f)
			}
		}
	})

	t.Run("seaweedfs variants resolve", func(t *testing.T) {
		if s, err := pickSource("seaweedfs"); err != nil {
			t.Errorf("seaweedfs: %v", err)
		} else if _, ok := s.(*eventingest.SeaweedFSSource); !ok {
			t.Errorf("seaweedfs: got %T", s)
		}
		if s, err := pickSource("seaweedfs_nats"); err != nil {
			t.Errorf("seaweedfs_nats: %v", err)
		} else if _, ok := s.(*eventingest.SeaweedFSNATSSource); !ok {
			t.Errorf("seaweedfs_nats: got %T", s)
		}
	})

	t.Run("garage is rejected with a directive error", func(t *testing.T) {
		_, err := pickSource("garage")
		if err == nil {
			t.Fatal("garage should be rejected — it emits no notifications")
		}
		// The message must point the operator at the real fix.
		if !strings.Contains(err.Error(), "Reconciler") {
			t.Errorf("garage error should mention the Reconciler, got: %v", err)
		}
	})

	t.Run("empty and unknown are errors", func(t *testing.T) {
		if _, err := pickSource(""); err == nil {
			t.Error("empty source_format should error")
		}
		if _, err := pickSource("nope"); err == nil {
			t.Error("unknown source_format should error")
		}
	})
}
