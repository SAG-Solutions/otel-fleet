package pipelines

import (
	"context"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"gopkg.in/yaml.v3"
)

// OtelCollectorGVR is the opentelemetry-operator CRD resource.
var OtelCollectorGVR = schema.GroupVersionResource{
	Group:    "opentelemetry.io",
	Version:  "v1beta1",
	Resource: "opentelemetrycollectors",
}

// K8sDistributor applies rendered configs by patching the `spec.config` of an
// OpenTelemetryCollector custom resource; the opentelemetry-operator then
// rolls the forwarding collector. Selected with OTEL_FLEET_DISTRIBUTOR=k8s.
type K8sDistributor struct {
	Client    dynamic.Interface
	Name      string // OTEL_FLEET_K8S_CR_NAME
	Namespace string // OTEL_FLEET_K8S_CR_NAMESPACE
}

var _ Distributor = (*K8sDistributor)(nil)

// NewK8sDistributor builds a distributor from in-cluster config, falling back
// to the local kubeconfig (dev convenience).
func NewK8sDistributor(name, namespace string) (*K8sDistributor, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{}).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("k8s distributor: no in-cluster config and no kubeconfig: %w", err)
		}
	}
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("k8s distributor: dynamic client: %w", err)
	}
	return &K8sDistributor{Client: client, Name: name, Namespace: namespace}, nil
}

// Distribute merge-patches spec.config of the CR. v1beta1 models config as a
// structured object, so the rendered YAML is decoded before patching.
func (d *K8sDistributor) Distribute(ctx context.Context, renderedFullConfig string) (string, string, error) {
	var cfg map[string]any
	if err := yaml.Unmarshal([]byte(renderedFullConfig), &cfg); err != nil {
		return "", "", fmt.Errorf("k8s distributor: decode rendered config: %w", err)
	}
	patch, err := json.Marshal(map[string]any{"spec": map[string]any{"config": cfg}})
	if err != nil {
		return "", "", fmt.Errorf("k8s distributor: marshal patch: %w", err)
	}
	_, err = d.Client.Resource(OtelCollectorGVR).Namespace(d.Namespace).
		Patch(ctx, d.Name, types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return "", "", fmt.Errorf("k8s distributor: patch %s/%s: %w", d.Namespace, d.Name, err)
	}
	return StateApplied, fmt.Sprintf("patched OpenTelemetryCollector %s/%s", d.Namespace, d.Name), nil
}
