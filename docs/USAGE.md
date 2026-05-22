# Usage Guide

Complete guide for deploying and configuring the Cilium Egress Operator.

---

## Table of Contents

- [Prerequisites](#prerequisites)
- [Installation](#installation)
- [Creating an EgressGateway](#creating-an-egressgateway)
- [Lease Sources](#lease-sources)
- [Candidate Filtering](#candidate-filtering)
- [Debounce Configuration](#debounce-configuration)
- [Monitoring](#monitoring)
- [Troubleshooting](#troubleshooting)
- [Uninstallation](#uninstallation)

---

## Prerequisites

1. **Kubernetes** 1.25 or later
2. **Cilium** with egress gateway enabled (`enableEgressGateway: true` in Cilium config)
3. A service using **Kubernetes Lease** for leader election (e.g. kube-vip, MetalLB, OpenELB)

Find existing Leases in your cluster:

```bash
kubectl get lease -A -o custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name,HOLDER:.spec.holderIdentity
```

---

## Installation

### Step 1: Install the CRD

```bash
kubectl apply -f config/crd/egressgateway.yaml
```

### Step 2: Deploy the Operator

Use Helm (recommended):

```bash
helm install cilium-egress-operator deploy/charts/cilium-egress-operator \
  --namespace kube-system \
  --set image.repository=your-registry/cilium-egress-operator \
  --set image.tag=v0.2.0
```

Or plain YAML:

```bash
kubectl apply -f deploy/manifest/rbac.yaml
kubectl apply -f deploy/manifest/deployment.yaml
kubectl apply -f deploy/manifest/service.yaml
kubectl apply -f deploy/manifest/pdb.yaml
```

### Step 3: Verify

```bash
kubectl get pods -n kube-system -l app=cilium-egress-operator
# Should show 3/3 Running
```

---

## Creating an EgressGateway

An EgressGateway defines the mapping between a Kubernetes Lease and a node label. The operator watches the Lease and moves the label to whichever node the Lease identifies as leader.

Typically you only need **one EgressGateway per cluster**, tracking a single Lease (e.g. kube-vip). Multiple business workloads use different `CiliumEgressGatewayPolicy` with different `egressIP` values, all selecting the same node label.

### Minimal Example (kube-vip)

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

The operator automatically resolves the Lease `holderIdentity` to a node:
1. Tries matching by node name (e.g. `node-1`)
2. If not found, searches node `status.addresses[Hostname]` (e.g. `node-1.example.com`)

Then sets `egress.cilium.io/gateway=true` on the resolved gateway node.

### Checking EgressGateway Status

```bash
kubectl get egw
```

Output:

```
NAME             CURRENT    DESIRED     AGE
egress-gateway   node-1     node-1      5m
```

```bash
kubectl get egw egress-gateway -o yaml
```

---

## Lease Sources

The operator works with any service that uses Kubernetes Lease for leader election. It is not tied to kube-vip or any specific product.

### kube-vip

kube-vip provides virtual IP failover for Kubernetes clusters. It uses a Lease to track which node owns the VIP.

```yaml
spec:
  leaseName: plndr-svcs-lock
  leaseNamespace: kube-system
```

### MetalLB

MetalLB is a load balancer for bare-metal Kubernetes. Its controller uses a Lease for leader election.

```yaml
spec:
  leaseName: controller-leader
  leaseNamespace: metallb-system
```

### OpenELB (Porter)

OpenELB provides load balancing for bare-metal clusters.

```yaml
spec:
  leaseName: porter-manager-leader-election
  leaseNamespace: porter-system
```

### Cilium Operator

Cilium Operator itself uses a Lease for HA. You can track which node is the Cilium operator leader.

```yaml
spec:
  leaseName: cilium-operator-resource-lock
  leaseNamespace: kube-system
```

### Any controller-runtime Operator

Any operator built with controller-runtime and `--leader-elect` creates a Lease named `<election-id>`.

```yaml
spec:
  leaseName: my-operator.leader
  leaseNamespace: my-operator-system
```

### Finding the Right Lease

If you're unsure which Lease to use:

```bash
# List all Leases with their holder identity
kubectl get lease -A -o custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name,HOLDER:.spec.holderIdentity
```

The `holderIdentity` should contain a node name or hostname that the operator can resolve.

---

## Candidate Filtering

Optionally restrict the gateway to a subset of nodes by setting `candidates`. If the resolved leader node is not in the list, `fallbackCandidate` is used instead.

### Example

```yaml
apiVersion: egress.cilium.io/v1alpha1
kind: EgressGateway
metadata:
  name: egress-gateway
spec:
  leaseName: plndr-svcs-lock
  leaseNamespace: kube-system
  nodeLabelKey: egress.cilium.io/gateway
  candidates:
    - gw-node-1
    - gw-node-2
    - gw-node-3
  fallbackCandidate: gw-node-1
  debounceDuration: "60s"
```

Behavior:
- If lease leader is `gw-node-2` (a candidate) → label goes to `gw-node-2`
- If lease leader is `worker-5` (not a candidate) → label goes to `gw-node-1` (fallback)
- If no fallback is defined and leader is not a candidate → reconcile fails with `selector_failed`

---

## Debounce Configuration

The `debounceDuration` field prevents flapping when the Lease leader is unstable.

### How It Works

1. Operator computes desired gateway node
2. If different from current gateway:
   - Records `desiredGatewayNode` and `desiredSince` in status
   - Waits for the full `debounceDuration`
   - If desired node changes during the wait, the timer resets
3. After the wait period, the switch is performed

### Example Timeline

```
T+0s   : lease leader = node-1 -> desired = node-1 (same as current, no-op)
T+10s  : lease leader = node-2 -> desired = node-2, debounce starts (30s)
T+15s  : lease leader = node-1 -> desired = node-1 (same as current, debounce cancelled)
T+20s  : lease leader = node-3 -> desired = node-3, debounce starts (30s)
T+50s  : debounce expired -> switch from node-1 to node-3
```

### Recommended Values

| Scenario | Recommended `debounceDuration` |
|----------|-------------------------------|
| Stable network | `"10s"` |
| Normal production | `"30s"` |
| Unstable / testing | `"60s"` or higher |
| No debounce needed | Omit the field |

---

## Requeue Interval

The `requeueInterval` field controls how quickly the operator retries after a reconcile failure (e.g. node not ready, label patch failed, lease not found). Defaults to `"5s"`.

```yaml
spec:
  requeueInterval: "3s"
```

| Scenario | Recommended `requeueInterval` |
|----------|-------------------------------|
| Fast failover needed | `"3s"` |
| Normal production | `"5s"` (default) |
| Reduce API server load | `"10s"` or higher |

---

## Monitoring

### Metrics Endpoint

Metrics are served at `http://<pod-ip>:8080/metrics`.

Configure Prometheus to scrape:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: cilium-egress-operator
  namespace: kube-system
spec:
  selector:
    matchLabels:
      app: cilium-egress-operator
  endpoints:
    - port: metrics
      interval: 15s
```

### Key Metrics

**Successful switches:**

```promql
rate(egress_switch_total{gateway="production-egress"}[5m])
```

**Failed switches by reason:**

```promql
sum by (reason) (increase(egress_switch_fail_total{gateway="production-egress"}[1h]))
```

**Current gateway node:**

```promql
egress_current_gateway{gateway="production-egress"}
```

**Leader change rate:**

```promql
rate(vip_leader_change_total[5m])
```

**Reconcile latency p99:**

```promql
histogram_quantile(0.99, rate(egress_reconcile_duration_seconds_bucket[5m]))
```

### Alerting

See [README.md](../README.md#prometheus-metrics) for example Prometheus alert rules.

---

## Troubleshooting

### Gateway not switching

1. Check EgressGateway status:

   ```bash
   kubectl get egw <egress-gateway-name> -o jsonpath='{.status}'
   ```

2. Check if the desired node is Ready:

   ```bash
   kubectl get node <node-name> -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'
   ```

3. Check debounce: if `desiredSince` is recent, the operator may be waiting for the debounce window.

4. Check operator logs:

   ```bash
   kubectl logs -n kube-system -l app=cilium-egress-operator --tail=100
   ```

### `selector_failed` errors

- Ensure the Lease `holderIdentity` matches a real Node name or hostname address
- If using `candidates`, ensure at least one candidate or `fallbackCandidate` exists
- Verify the Lease exists and has a holder identity:

   ```bash
   kubectl get lease <lease-name> -n <lease-namespace> -o yaml
   ```

### `node_not_ready` errors

The desired gateway node is not in `Ready` state. The operator will retry every 10 seconds. Check:

```bash
kubectl describe node <node-name>
```

### `patch_failed` errors

Node label patch failed, likely due to a concurrent modification. The operator will retry. If persistent:

```bash
kubectl get node <node-name> -o yaml | grep -A5 labels
```

Check for label conflicts.

### No active gateway

If `egress_current_gateway` is empty for the gateway:

1. Verify the Lease exists and has a `holderIdentity`:

   ```bash
   kubectl get lease <lease-name> -n <lease-namespace> -o yaml
   ```

2. Verify the EgressGateway spec references the correct Lease

3. Check operator metrics for failures

---

## Cilium Egress Gateway Policy Integration

After creating an EgressGateway, configure Cilium to use the node label for egress routing:

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
    egressIP: "203.0.113.50"
```

When the operator moves the label to a different node, Cilium automatically re-routes egress traffic through the new gateway.

### Multiple Business Workloads

Different workloads can share the same gateway node but use different egress IPs. Just create multiple `CiliumEgressGatewayPolicy` resources with different `egressIP` values, all selecting the same node label:

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

All policies share the same gateway node (the Lease leader), but each routes traffic through a different egress IP. No need for multiple gateways or multiple LB components.

---

## Uninstallation

1. Delete the EgressGateway:

   ```bash
   kubectl delete egw --all
   ```

2. Delete the operator deployment:

   ```bash
   kubectl delete deployment -n kube-system cilium-egress-operator
   ```

3. Delete RBAC resources:

   ```bash
   kubectl delete clusterrolebinding cilium-egress-operator
   kubectl delete clusterrole cilium-egress-operator
   kubectl delete serviceaccount -n kube-system cilium-egress-operator
   ```

4. Delete the CRD (this removes all EgressGateway resources):

   ```bash
   kubectl delete crd egressgateways.egress.cilium.io
   ```

5. Remove node labels manually if needed:

   ```bash
   kubectl get nodes -o name | while read node; do
     kubectl label $node egress.cilium.io/gateway- 2>/dev/null
   done
   ```
