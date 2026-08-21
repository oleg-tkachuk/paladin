package app

import (
	"net/http"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/middleware"
)

// BuildMetricsListener returns the Prometheus scrape endpoint, or nil when
// metrics are not exported by pull.
//
// Its own listener, in plain HTTP, for the reason config.OTel.MetricsAddr
// documents: the API planes serve TLS from an internal CA, and reaching them
// would mean telling the collector to skip verification for every target in
// the cluster. Garage and SeaweedFS are already scraped this way, on 3903 and
// 9327 — this follows the shape that exists rather than asking the shared
// collector to accommodate one service.
//
// It serves exactly one path. A scraper that finds anything else here is
// pointed at the wrong port, and should be told so rather than handed a 200.
func BuildMetricsListener(deps *SharedDeps) *HTTPListener {
	if deps == nil || deps.Metrics == nil {
		return nil
	}
	addr := deps.Cfg.OTel.MetricsAddr
	if addr == "" {
		addr = "0.0.0.0:9095"
	}

	mux := http.NewServeMux()
	mux.Handle(middleware.PathMetrics, deps.Metrics)

	return &HTTPListener{
		Plane: "metrics",
		Server: &http.Server{
			Addr:    addr,
			Handler: mux,
			// Short and fixed rather than config-driven: a scrape is a small
			// GET on a loopback-adjacent network, and the endpoint has no
			// other traffic to accommodate.
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
		// Deliberately no TLS: see above. Metrics carry no credentials and the
		// port is not exposed beyond the pod network.
		TLS: config.TLS{Enabled: false},
	}
}
