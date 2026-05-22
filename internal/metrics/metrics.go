package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	ReasonNodeNotReady   = "node_not_ready"
	ReasonPatchFailed    = "patch_failed"
	ReasonSelectorFailed = "selector_failed"
)

var (
	SwitchTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "egress_switch_total",
			Help: "Total number of successful gateway switches",
		},
		[]string{"gateway"},
	)

	SwitchFailTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "egress_switch_fail_total",
			Help: "Total number of failed gateway switches",
		},
		[]string{"gateway", "reason"},
	)

	CurrentGateway = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "egress_current_gateway",
			Help: "Current egress gateway node (1 = active)",
		},
		[]string{"gateway", "node"},
	)

	LeaderChangeTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "vip_leader_change_total",
			Help: "Total number of Lease leader changes observed",
		},
	)

	ReconcileDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "egress_reconcile_duration_seconds",
			Help:    "Reconcile latency in seconds",
			Buckets: prometheus.DefBuckets,
		},
	)
)

func init() {
	metrics.Registry.MustRegister(SwitchTotal)
	metrics.Registry.MustRegister(SwitchFailTotal)
	metrics.Registry.MustRegister(CurrentGateway)
	metrics.Registry.MustRegister(LeaderChangeTotal)
	metrics.Registry.MustRegister(ReconcileDuration)
}

func SetCurrentGateway(gateway, node string) {
	CurrentGateway.Reset()
	CurrentGateway.WithLabelValues(gateway, node).Set(1)
}

func IncSwitch(gateway string) {
	SwitchTotal.WithLabelValues(gateway).Inc()
}

func IncSwitchFail(gateway, reason string) {
	SwitchFailTotal.WithLabelValues(gateway, reason).Inc()
}

func IncLeaderChange() {
	LeaderChangeTotal.Inc()
}
