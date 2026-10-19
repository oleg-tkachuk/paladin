// Package testimage names the images the integration suites start besides
// Postgres (internal/pgtest), and makes sure one is present before a suite
// asks testcontainers for a container.
//
// testcontainers pulls a missing image itself, but reads the pull's response
// to its end without decoding it. The daemon reports a failed pull — a
// registry's rate limit, a transient error — as a message inside that stream,
// not as the HTTP status, so testcontainers takes it for a pull that worked
// and the container create that follows fails with "No such image", with
// nothing logged about the pull. Ensure decodes the stream, checks that the
// image is there afterwards, and tries again a few times.
package testimage

import (
	"context"
	"fmt"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
)

// The images the suites start, each in one place for every suite using it.
const (
	ElasticMQ = "softwaremill/elasticmq-native:1.6.11"
	RabbitMQ  = "rabbitmq:4.0-alpine"
	Redpanda  = "docker.redpanda.com/redpandadata/redpanda:v24.3.6"
)

const (
	// pullAttempts bounds the tries at a pull that fails or leaves no image.
	pullAttempts = 4
	// firstRetryDelay is the wait before the second try, doubled each time.
	firstRetryDelay = 2 * time.Second
)

// docker is what Ensure needs of a Docker client; *client.Client has it.
type docker interface {
	ImageInspect(ctx context.Context, image string, opts ...client.ImageInspectOption) (client.ImageInspectResult, error)
	ImagePull(ctx context.Context, ref string, opts client.ImagePullOptions) (client.ImagePullResponse, error)
}

// Ensure makes ref present on the Docker host testcontainers uses, pulling it
// when it is not. A pull the daemon reports as failed, or one after which the
// image is still absent, is tried again; the last error is returned.
func Ensure(ctx context.Context, ref string) error {
	cli, err := testcontainers.NewDockerClientWithOpts(ctx)
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}
	defer func() { _ = cli.Close() }()
	return ensure(ctx, cli, ref, sleep)
}

func ensure(ctx context.Context, d docker, ref string, wait func(context.Context, time.Duration) error) error {
	present, err := isPresent(ctx, d, ref)
	if err != nil || present {
		return err
	}
	delay := firstRetryDelay
	for attempt := 1; ; attempt++ {
		err = pull(ctx, d, ref)
		if err == nil {
			present, err = isPresent(ctx, d, ref)
			if err == nil && present {
				return nil
			}
			if err == nil {
				err = fmt.Errorf("pull of %s finished, but the image is not there", ref)
			}
		}
		if attempt == pullAttempts {
			return fmt.Errorf("image %s after %d attempts: %w", ref, pullAttempts, err)
		}
		if werr := wait(ctx, delay); werr != nil {
			return werr
		}
		delay *= 2
	}
}

// isPresent reports whether the host holds ref; any error but not-found ends
// the attempt.
func isPresent(ctx context.Context, d docker, ref string) (bool, error) {
	_, err := d.ImageInspect(ctx, ref)
	switch {
	case err == nil:
		return true, nil
	case cerrdefs.IsNotFound(err):
		return false, nil
	default:
		return false, fmt.Errorf("inspect %s: %w", ref, err)
	}
}

// pull runs one pull to its end; Wait returns the error the daemon reports
// inside the stream.
func pull(ctx context.Context, d docker, ref string) error {
	resp, err := d.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pull %s: %w", ref, err)
	}
	if err := resp.Wait(ctx); err != nil {
		return fmt.Errorf("pull %s: %w", ref, err)
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
