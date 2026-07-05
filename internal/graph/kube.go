package graph

import (
	"fmt"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// kubeClient bundles the typed clientset (built-in kinds), the dynamic
// client (CRDs — Flux today, generic CRDs in milestone 5) and metadata
// identifying which cluster they talk to.
type kubeClient struct {
	clientset kubernetes.Interface
	dynamic   dynamic.Interface
	meta      ClusterMeta
}

// newKubeClient loads the active kubeconfig — same resolution rules as
// kubectl: $KUBECONFIG, then ~/.kube/config, honoring the current context.
// Building one per discovery pass is fine: it only parses a local file; no
// network happens until the first API call.
func newKubeClient() (*kubeClient, error) {
	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{})

	raw, err := loader.RawConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	cfg, err := loader.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("build client config: %w", err)
	}

	// A discovery pass fires many small LIST calls back to back; client-go's
	// default rate limit (QPS 5) would throttle that artificially. This is
	// read-only load against one cluster, so a higher ceiling is safe.
	cfg.QPS = 50
	cfg.Burst = 100
	// Don't hang forever on an unreachable cluster — fail the pass instead.
	cfg.Timeout = 15 * time.Second

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("create clientset: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("create dynamic client: %w", err)
	}
	return &kubeClient{
		clientset: cs,
		dynamic:   dyn,
		meta:      ClusterMeta{Context: raw.CurrentContext, Server: cfg.Host},
	}, nil
}
