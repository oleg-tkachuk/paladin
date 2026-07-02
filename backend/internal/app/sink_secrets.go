package app

import (
	"context"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// sinkSecretResolver adapts config.K8sSecretResolver (SecretRef-shaped) to
// worker.SinkSecretResolver (primitive-shaped — the worker package stays
// config-free). Used by every pod that runs sink deliveries: the dispatcher
// pod's drain loop and the admin pod's synchronous TestSubscription.
type sinkSecretResolver struct {
	r *config.K8sSecretResolver
}

func (s sinkSecretResolver) ResolveSinkSecret(ctx context.Context, namespace, name, key string) (string, error) {
	return s.r.ResolveSecret(ctx, &config.SecretRef{Namespace: namespace, Name: name, Key: key})
}

// NewSinkSecretResolver builds the delivery-time resolver for "k8s:" refs in
// sink-credential fields (see worker/sink_secrets.go). Out-of-cluster the
// underlying resolver errors on use — which is correct: a k8s: ref simply
// cannot be honoured there, and the delivery fails with a clear reason.
func NewSinkSecretResolver(l *zap.Logger) worker.SinkSecretResolver {
	return sinkSecretResolver{r: config.NewK8sSecretResolver(l)}
}
