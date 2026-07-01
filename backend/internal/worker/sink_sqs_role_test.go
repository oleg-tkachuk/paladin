package worker

import (
	"context"
	"testing"
)

// role_arn cross-account delivery: the client pool keys by (region, roleArn)
// and threads roleArn through to newClient (where the production path assumes
// the role). Verified via the newClient seam — no AWS / STS needed.

func TestSQSPool_KeyedByRegionAndRole(t *testing.T) {
	pool := NewSQSClientPool(nil)
	var calls []string
	pool.newClient = func(_ context.Context, region, roleArn string) (sqsSender, error) {
		calls = append(calls, region+"|"+roleArn)
		return &fakeSQS{}, nil
	}

	ctx := context.Background()
	// Same region, different roles → two distinct builds.
	if _, err := pool.get(ctx, "us-east-1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.get(ctx, "us-east-1", "arn:aws:iam::999:role/deliver"); err != nil {
		t.Fatal(err)
	}
	// Repeat of the first (region, role) → cached, no new build.
	if _, err := pool.get(ctx, "us-east-1", ""); err != nil {
		t.Fatal(err)
	}
	// Same role, different region → distinct.
	if _, err := pool.get(ctx, "eu-west-1", "arn:aws:iam::999:role/deliver"); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"us-east-1|",
		"us-east-1|arn:aws:iam::999:role/deliver",
		"eu-west-1|arn:aws:iam::999:role/deliver",
	}
	if len(calls) != len(want) {
		t.Fatalf("newClient calls = %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Errorf("call[%d] = %q, want %q", i, calls[i], want[i])
		}
	}
}

func TestDeliverSQS_ThreadsRoleArn(t *testing.T) {
	pool := NewSQSClientPool(nil)
	var gotRole string
	pool.newClient = func(_ context.Context, _ string, roleArn string) (sqsSender, error) {
		gotRole = roleArn
		return &fakeSQS{}, nil
	}
	d := &Dispatcher{SQS: pool}
	sub := sqsTestSub(t, sqsSinkConfig{
		QueueURL: "https://sqs.us-east-1.amazonaws.com/999/q",
		Region:   "us-east-1",
		RoleArn:  "arn:aws:iam::999:role/deliver",
	})

	if _, err := d.deliverSQS(context.Background(), sub, sinkTestEvent()); err != nil {
		t.Fatalf("deliverSQS: %v", err)
	}
	if gotRole != "arn:aws:iam::999:role/deliver" {
		t.Errorf("role_arn threaded to newClient = %q, want the sink's role", gotRole)
	}
}

// No role_arn → the pool still builds a (same-account) client with an empty
// role, unchanged from the pre-cross-account behaviour.
func TestDeliverSQS_NoRoleArnIsSameAccount(t *testing.T) {
	pool := NewSQSClientPool(nil)
	var gotRole = "sentinel"
	pool.newClient = func(_ context.Context, _ string, roleArn string) (sqsSender, error) {
		gotRole = roleArn
		return &fakeSQS{}, nil
	}
	d := &Dispatcher{SQS: pool}
	sub := sqsTestSub(t, sqsSinkConfig{
		QueueURL: "https://sqs.us-east-1.amazonaws.com/123/q",
		Region:   "us-east-1",
	})
	if _, err := d.deliverSQS(context.Background(), sub, sinkTestEvent()); err != nil {
		t.Fatalf("deliverSQS: %v", err)
	}
	if gotRole != "" {
		t.Errorf("role_arn = %q, want empty for a same-account sink", gotRole)
	}
}
