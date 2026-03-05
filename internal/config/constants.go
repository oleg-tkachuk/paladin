package config

const (
	// DefaultK8sServiceHostEnvKey is the standard Kubernetes environment variable
	// used to detect if the application is running inside a cluster.
	DefaultK8sServiceHostEnvKey = "KUBERNETES_SERVICE_HOST"

	// DefaultK8sNamespaceEnvKey is the standard environment variable holding the
	// Kubernetes namespace the pod is deployed in.
	DefaultK8sNamespaceEnvKey = "K8S_NAMESPACE"
)
