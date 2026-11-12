package config

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// RuntimeSecretResolver reads the Secrets a running process names from data
// rather than from its config — a registered storage backend's credentials,
// an event sink's — and only from runtime_secrets.namespace when that is set
// (ADR-0047): the one namespace where the chart lets the admin plane and the
// dispatcher read Secrets, so a ref cannot reach the release's own, such as
// the session key.
type RuntimeSecretResolver struct {
	k8s       *K8sSecretResolver
	namespace string
}

// NewRuntimeSecretResolver reads Secrets from namespace; empty keeps a ref
// where it names, else in the pod's namespace.
func NewRuntimeSecretResolver(log *zap.Logger, namespace string) *RuntimeSecretResolver {
	return &RuntimeSecretResolver{k8s: NewK8sSecretResolver(log), namespace: namespace}
}

// ResolveSecret reads ref, in the runtime namespace when one is set: a ref
// naming no namespace is read there, one naming another is refused.
func (r *RuntimeSecretResolver) ResolveSecret(ctx context.Context, ref *SecretRef) (string, error) {
	resolved := *ref
	if r.namespace != "" {
		switch resolved.Namespace {
		case "":
			resolved.Namespace = r.namespace
		case r.namespace:
		default:
			// The ref's own namespace and name stay out of the line: a ref is
			// read from a credential field, and its caller logs the error
			// below, which names them.
			r.k8s.log.Warn("refused a runtime secret outside the runtime namespace",
				zap.String("runtime_namespace", r.namespace))
			return "", fmt.Errorf("secret %s/%s: runtime secrets are read from namespace %q only (runtime_secrets.namespace)",
				resolved.Namespace, resolved.Name, r.namespace)
		}
	}
	return r.k8s.ResolveSecret(ctx, &resolved)
}
