package app

import (
	"net/http"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/rest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
)

func newQuotaManager(cfg *rest.Config, provider multicluster.Provider, logger logr.Logger, mapper meta.RESTMapper) (mcmanager.Manager, error) {
	return mcmanager.New(cfg, provider, mcmanager.Options{
		Scheme: Scheme,
		Logger: logger,
		// Share the controller context's periodically reset discovery mapper.
		// The default dynamic mapper can retain a group with no versions after
		// stale aggregated discovery, preventing ownership lookups from recovering.
		MapperProvider: func(_ *rest.Config, _ *http.Client) (meta.RESTMapper, error) {
			return mapper, nil
		},
		Metrics: metricsserver.Options{
			BindAddress: "0", // Avoid port conflict with main manager.
		},
	})
}
