# Cilium Egress Operator

[![Go Version](https://img.shields.io/github/go-mod/go-version/jinxf0120/cilium-egress-operator)](https://github.com/jinxf0120/cilium-egress-operator)
[![License](https://img.shields.io/github/license/jinxf0120/cilium-egress-operator)](LICENSE)
[![Kubernetes](https://img.shields.io/badge/kubernetes-1.25+-blue)](https://kubernetes.io)
[![Cilium](https://img.shields.io/badge/cilium-egress%20gateway-3f8fc9)](https://cilium.io)

Automatic failover for [Cilium Egress Gateway](https://docs.cilium.io/en/stable/network/egress-gateway/) (OSS) based on Kubernetes Lease leader election.

Works with any service that uses Kubernetes Lease for leader election — kube-vip, MetalLB, OpenELB, Cilium Operator, or any controller-runtime based operator.

---

## Features

- **HA operator** - Multiple replicas with controller-runtime leader election; only the leader reconciles
- **Lease-based leader tracking** - Watches any Kubernetes Lease to detect leader changes in real time
- **Product-agnostic** - Works with kube-vip, MetalLB, OpenELB, or any Lease-based leader election
- **Auto node resolution** - Resolves Lease holder identity by node name, falling back to hostname matching
- **Candidate filtering** - Optionally restrict gateway to a subset of nodes with fallback
- **Debounce** - Configurable cooldown to prevent flapping
- **Idempotent** - Reconcile is deterministic; no-op when desired == current
- **Prometheus metrics** - Switch counts, failure reasons, current gateway, leader changes, reconcile latency

---

## Architecture

```
Lease (leader election)
        |
  operator (leader instance only)
        |
  gateway selection
        |
  node label mutation
        |
  cilium egress gateway routing
```

The operator itself is stateless. System state is defined only by:

- Lease (leader identity from any source)
- Node labels (gateway assignment)
- `EgressGateway` CR (intent)

---

## Quick Start

### Prerequisites

- Kubernetes 1.25+
- Cilium with egress gateway enabled
- A service using Kubernetes Lease for leader election (e.g. kube-vip, MetalLB, OpenELB)

### Install with Helm (Recommended)

```bash
helm install cilium-egress-operator deploy/charts/cilium-egress-operator \
  --namespace kube-system \
  --set image.repository=your-registry/cilium-egress-operator \
  --set image.tag=v0.2.0
```

Or install with plain YAML:

```bash
kubectl apply -f config/crd/egressgateway.yaml
kubectl apply -f deploy/manifest/rbac.yaml
kubectl apply -f deploy/manifest/deployment.yaml
kubectl apply -f deploy/manifest/service.yaml
kubectl apply -f deploy/manifest/pdb.yaml
```

### Create an EgressGateway

**Example with kube-vip:**

```yaml
apiVersion: egress.cilium.io/v1alpha1
kind: EgressGateway
metadata:
  name: egress-gateway
spec:
  leaseName: plndr-svcs-lock
  leaseNamespace: kube-system
  nodeLabelKey: egress.cilium.io/gateway
  debounceDuration: "30s"
```

This creates an EgressGateway that follows the kube-vip Lease. When kube-vip's VIP drifts to another node, the operator moves the `egress.cilium.io/gateway` label to the new node.

Multiple business workloads can share the same gateway by creating different `CiliumEgressGatewayPolicy` resources with different `egressIP` values, all selecting the same node label:

```yaml
# Payment service → egress IP 10.0.1.100
apiVersion: cilium.io/v2
kind: CiliumEgressGatewayPolicy
metadata:
  name: payment-egress
spec:
  egressGateway:
    nodeSelector:
      matchLabels:
        egress.cilium.io/gateway: "true"
  destinationCIDRs: ["0.0.0.0/0"]
  egressIP:
    egressIP: "10.0.1.100"
---
# API gateway → egress IP 10.0.2.200
apiVersion: cilium.io/v2
kind: CiliumEgressGatewayPolicy
metadata:
  name: api-gw-egress
spec:
  egressGateway:
    nodeSelector:
      matchLabels:
        egress.cilium.io/gateway: "true"
  destinationCIDRs: ["0.0.0.0/0"]
  egressIP:
    egressIP: "10.0.2.200"
```

All policies share the same gateway node (the Lease leader), but each uses a different egress IP. No need for multiple gateways or multiple LB components.

---

## Supported Lease Sources

Any service that uses Kubernetes Lease for leader election can be tracked. Common examples:

| Product | Default Lease Name | Namespace |
|---------|-------------------|-----------|
| **kube-vip** | `plndr-svcs-lock` | `kube-system` |
| **MetalLB** | `controller-leader` | `metallb-system` |
| **OpenELB** (Porter) | `porter-manager-leader-election` | `porter-system` |
| **Cilium Operator** | `cilium-operator-resource-lock` | `kube-system` |
| **Any controller-runtime operator** | `<name>.<group>` | operator namespace |

To find the Lease name for your service:

```bash
kubectl get lease -A
```

---

## Gateway Selection

The operator automatically resolves the Lease `holderIdentity` to a node:

1. **By node name** - If `holderIdentity` matches a Kubernetes Node name, use it directly
2. **By hostname** - If no node name matches, search all nodes' `status.addresses[Hostname]` for a match

This covers all common scenarios (short names, FQDNs, cloud provider hostnames) without configuration.

### Candidate Filtering

Optionally restrict the gateway to a subset of nodes by setting `candidates`. If the resolved node is not in the list, `fallbackCandidate` is used instead.

```yaml
spec:
  nodeLabelKey: egress.cilium.io/gateway
  candidates:
    - node-1
    - node-2
    - node-3
  fallbackCandidate: node-1
```

---

## CR Reference: EgressGateway

### Spec

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `leaseName` | string | Yes | Name of the Kubernetes Lease to watch |
| `leaseNamespace` | string | Yes | Namespace of the Lease |
| `nodeLabelKey` | string | Yes | Label key to set on the gateway node |
| `nodeLabelValue` | string | No | Label value (defaults to `"true"`) |
| `candidates` | string[] | No | List of candidate node names. When set, gateway is only assigned to nodes in this list |
| `fallbackCandidate` | string | No | Fallback node when leader is not in the candidates list |
| `debounceDuration` | string | No | Minimum duration between gateway switches (e.g. `"30s"`). Prevents flapping. |
| `requeueInterval` | string | No | Interval for retrying on reconcile failure (e.g. `"5s"`). Defaults to `"5s"`. |

### Status

| Field | Type | Description |
|-------|------|-------------|
| `currentGatewayNode` | string | Node currently holding the gateway label |
| `desiredGatewayNode` | string | Node the operator wants to assign the gateway to |
| `desiredSince` | datetime | When the desired node was first computed (used for debounce) |
| `lastSwitchTime` | datetime | Timestamp of the last successful gateway switch |
| `observedGeneration` | int64 | Last observed generation of the spec |

---

## Reconcile Algorithm

The operator:

1. Reads the Lease `holderIdentity`
2. Resolves the desired gateway node (by node name, then hostname)
3. If `candidates` is set, applies candidate filtering
4. Checks if the desired node is `Ready`
5. Reads current node labels to find which node holds the gateway label
6. **If desired == current** → no-op (idempotent)
7. **If different and debounce is active** → wait for debounce window to expire
8. **If different and debounce expired** → remove label from old node, add label to new node

All operations are retry-safe. Node patch uses strategic merge patch to avoid conflicts.

---

## Debounce (Anti-Flapping)

When `debounceDuration` is set (e.g. `"30s"`), the operator waits for the full duration after computing a new desired gateway before actually switching. If the desired node changes again during the wait period, the timer resets.

This prevents rapid back-and-forth switching (flapping) when the Lease leader is unstable.

---

## Operator HA

- Deploy multiple replicas for availability
- Only the leader instance reconciles (controller-runtime Lease-based election)
- Passive replicas serve metrics and health probes
- Leader election ID: `cilium-egress-operator.leader`

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--metrics-bind-address` | `:8080` | Prometheus metrics endpoint |
| `--health-probe-bind-address` | `:8081` | Health probe endpoint |
| `--leader-elect` | `false` | Enable leader election for HA |

---

## Prometheus Metrics

All metrics are exposed at `:8080/metrics`.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `egress_switch_total` | Counter | `gateway` | Successful gateway switches |
| `egress_switch_fail_total` | Counter | `gateway`, `reason` | Failed gateway switches |
| `egress_current_gateway` | Gauge | `gateway`, `node` | Current gateway node (value=1) |
| `vip_leader_change_total` | Counter | - | Lease leader changes observed |
| `egress_reconcile_duration_seconds` | Histogram | - | Reconcile latency |

### Failure Reasons

| Reason | Description |
|--------|-------------|
| `node_not_ready` | Desired gateway node is not in `Ready` state |
| `patch_failed` | Node label patch operation failed |
| `selector_failed` | Gateway selection could not determine a node |

### Example Prometheus Rules

```yaml
groups:
  - name: egress-gateway
    rules:
      - alert: EgressGatewayFlapping
        expr: increase(egress_switch_total[5m]) > 3
        for: 1m
        labels:
          severity: warning
        annotations:
          summary: "Egress gateway flapping for gateway {{ $labels.gateway }}"

      - alert: EgressSwitchFailures
        expr: increase(egress_switch_fail_total[5m]) > 0
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "Egress switch failures for gateway {{ $labels.gateway }} (reason: {{ $labels.reason }})"

      - alert: EgressNoActiveGateway
          expr: count(egress_current_gateway) by (gateway) == 0
        for: 2m
        labels:
          severity: critical
        annotations:
          summary: "No active egress gateway for gateway {{ $labels.gateway }}"
```

---

## Cilium Integration

The operator sets node labels. Configure Cilium `CiliumEgressGatewayPolicy` to select egress gateway nodes using those labels:

```yaml
apiVersion: cilium.io/v2
kind: CiliumEgressGatewayPolicy
metadata:
  name: egress-policy
spec:
  egressGateway:
    nodeSelector:
      matchLabels:
        egress.cilium.io/gateway: "true"
  destinationCIDRs:
    - 0.0.0.0/0
  egressIP:
    egressIP: "10.0.0.100"
```

When the operator moves the label to a different node, Cilium automatically re-routes egress traffic.

**Multiple business workloads**: Create multiple `CiliumEgressGatewayPolicy` resources with different `egressIP` values, all selecting the same node label. Each policy routes its traffic through a different egress IP on the same gateway node. No need for multiple gateways.

---

## Build

```bash
make build          # Build binary
make docker         # Build container image
make test           # Run tests
make lint           # Run linter
make vet            # Run go vet
make fmt            # Format code
make manifests      # Regenerate CRD manifests
make clean          # Remove build artifacts
```

---

## Project Structure

```
.
├── main.go                          # Entry point
├── api/v1alpha1/                    # CRD types
│   ├── egressgateway_types.go
│   ├── groupversion_info.go
│   └── zz_generated.deepcopy.go
├── internal/
│   ├── controller/                  # EgressGateway reconciler
│   │   └── egressgateway_controller.go
│   ├── gateway/                     # Gateway selection logic
│   │   └── selector.go
│   └── metrics/                     # Prometheus metrics
│       └── metrics.go
├── deploy/
│   ├── charts/cilium-egress-operator/ # Helm chart
│   │   ├── Chart.yaml
│   │   ├── values.yaml
│   │   ├── crds/
│   │   └── templates/
│   └── manifest/                    # Plain Kubernetes manifests
│       ├── rbac.yaml
│       ├── deployment.yaml
│       ├── service.yaml
│       └── pdb.yaml
├── config/
│   └── crd/                         # CRD YAML manifests
│       └── egressgateway.yaml
├── docs/                            # Design & observability specs
│   ├── SPEC.md
│   ├── OBS.md
│   └── USAGE.md
├── Makefile
└── Dockerfile
```

---

## Docs

| Document | Description |
|----------|-------------|
| [README.md](README.md) | Project overview |
| [AGENTS.md](AGENTS.md) | Agent instructions and project goals |
| [SPEC.md](docs/SPEC.md) | System design specification |
| [OBS.md](docs/OBS.md) | Observability specification |
| [USAGE.md](docs/USAGE.md) | Detailed usage guide with examples |

---

## Contributing

Contributions are welcome! Please open an issue or submit a pull request.

1. Fork the repository
2. Create a feature branch (`git checkout -b feat/my-feature`)
3. Commit your changes (`git commit -m 'feat: add my feature'`)
4. Push to the branch (`git push origin feat/my-feature`)
5. Open a Pull Request

### Development

```bash
make build          # Build binary
make docker         # Build container image
make test           # Run tests
make lint           # Run linter
make vet            # Run go vet
make manifests      # Regenerate CRD manifests
```

---

## License

[Apache License 2.0](LICENSE)
