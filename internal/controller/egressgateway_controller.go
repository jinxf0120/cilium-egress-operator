package controller

import (
	"context"
	"fmt"
	"time"

	egressv1alpha1 "github.com/jinxf0120/cilium-egress-operator/api/v1alpha1"
	"github.com/jinxf0120/cilium-egress-operator/internal/gateway"
	"github.com/jinxf0120/cilium-egress-operator/internal/metrics"

	corev1 "k8s.io/api/core/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type EgressGatewayReconciler struct {
	client.Client
	Selector *gateway.Selector
}

func (r *EgressGatewayReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	start := time.Now()
	defer func() {
		metrics.ReconcileDuration.Observe(time.Since(start).Seconds())
	}()

	logger := log.FromContext(ctx)

	var egw egressv1alpha1.EgressGateway
	if err := r.Get(ctx, req.NamespacedName, &egw); err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	var egwList egressv1alpha1.EgressGatewayList
	if err := r.List(ctx, &egwList); err == nil && len(egwList.Items) > 1 {
		logger.Error(fmt.Errorf("multiple EgressGateway resources found (%d), only one is allowed", len(egwList.Items)), "singleton validation failed")
		return reconcile.Result{}, nil
	}

	logger.Info("reconciling egress gateway", "lease", egw.Spec.LeaseNamespace+"/"+egw.Spec.LeaseName, "labelKey", egw.Spec.NodeLabelKey, "generation", egw.Generation)

	leaseIdentity, err := r.readLeaseHolder(ctx, &egw)
	if err != nil {
		logger.Error(err, "failed to read lease holder", "lease", egw.Spec.LeaseNamespace+"/"+egw.Spec.LeaseName)
		metrics.IncSwitchFail(egw.Name, metrics.ReasonSelectorFailed)
		return reconcile.Result{RequeueAfter: r.requeueInterval(&egw)}, nil
	}
	logger.Info("lease holder resolved", "lease", egw.Spec.LeaseNamespace+"/"+egw.Spec.LeaseName, "holder", leaseIdentity)

	desiredNode, err := r.Selector.Select(ctx, &egw, leaseIdentity)
	if err != nil {
		logger.Error(err, "gateway selection failed", "holder", leaseIdentity)
		metrics.IncSwitchFail(egw.Name, metrics.ReasonSelectorFailed)
		return reconcile.Result{RequeueAfter: r.requeueInterval(&egw)}, nil
	}
	logger.Info("desired gateway node selected", "node", desiredNode, "holder", leaseIdentity)

	var desiredNodeObj corev1.Node
	if err := r.Get(ctx, types.NamespacedName{Name: desiredNode}, &desiredNodeObj); err != nil {
		logger.Error(err, "desired gateway node not found", "node", desiredNode)
		metrics.IncSwitchFail(egw.Name, metrics.ReasonSelectorFailed)
		return reconcile.Result{RequeueAfter: r.requeueInterval(&egw)}, nil
	}

	if !gateway.IsNodeReady(&desiredNodeObj) {
		logger.Info("desired gateway node is not ready", "node", desiredNode)
		metrics.IncSwitchFail(egw.Name, metrics.ReasonNodeNotReady)
		return reconcile.Result{RequeueAfter: r.requeueInterval(&egw)}, nil
	}

	currentNode, err := r.findCurrentGatewayNode(ctx, &egw)
	if err != nil {
		logger.Error(err, "failed to find current gateway node")
		return reconcile.Result{Requeue: true}, err
	}
	logger.Info("current gateway node", "node", currentNode, "desired", desiredNode)

	if currentNode == desiredNode {
		logger.Info("gateway unchanged, skipping switch", "node", desiredNode)
		if err := r.updateStatus(ctx, &egw, desiredNode); err != nil {
			logger.Error(err, "failed to update egress gateway status")
		}
		metrics.SetCurrentGateway(egw.Name, desiredNode)
		return reconcile.Result{}, nil
	}

	debounce := r.debounceDuration(&egw)
	if debounce > 0 && r.shouldDebounce(&egw, desiredNode, debounce) {
		remaining := r.remainingDebounce(&egw, desiredNode, debounce)
		if remaining > 0 {
			logger.Info("debouncing gateway switch", "from", currentNode, "to", desiredNode, "remaining", remaining)
			if err := r.setDesiredInStatus(ctx, &egw, desiredNode); err != nil {
				logger.Error(err, "failed to update desired in status")
			}
			return reconcile.Result{RequeueAfter: remaining}, nil
		}
	}

	logger.Info("switching gateway", "from", currentNode, "to", desiredNode, "labelKey", egw.Spec.NodeLabelKey)
	if err := r.switchGateway(ctx, &egw, currentNode, desiredNode); err != nil {
		logger.Error(err, "gateway switch failed", "from", currentNode, "to", desiredNode)
		metrics.IncSwitchFail(egw.Name, metrics.ReasonPatchFailed)
		return reconcile.Result{RequeueAfter: r.requeueInterval(&egw)}, nil
	}

	logger.Info("gateway switched", "gateway", egw.Name, "from", currentNode, "to", desiredNode)
	metrics.IncSwitch(egw.Name)
	metrics.SetCurrentGateway(egw.Name, desiredNode)

	now := metav1.Now()
	egw.Status.CurrentGatewayNode = desiredNode
	egw.Status.DesiredGatewayNode = desiredNode
	egw.Status.DesiredSince = &now
	egw.Status.LastSwitchTime = &now
	egw.Status.ObservedGeneration = egw.Generation
	if err := r.Status().Update(ctx, &egw); err != nil {
		logger.Error(err, "failed to update egress gateway status after switch")
	}

	return reconcile.Result{}, nil
}

func (r *EgressGatewayReconciler) readLeaseHolder(ctx context.Context, egw *egressv1alpha1.EgressGateway) (string, error) {
	logger := log.FromContext(ctx)
	var lease coordinationv1.Lease
	key := types.NamespacedName{
		Name:      egw.Spec.LeaseName,
		Namespace: egw.Spec.LeaseNamespace,
	}
	if err := r.Get(ctx, key, &lease); err != nil {
		return "", fmt.Errorf("get lease %s/%s: %w", egw.Spec.LeaseNamespace, egw.Spec.LeaseName, err)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity == "" {
		logger.Info("lease has no holder identity", "lease", egw.Spec.LeaseNamespace+"/"+egw.Spec.LeaseName)
		return "", fmt.Errorf("lease %s/%s has no holder identity", egw.Spec.LeaseNamespace, egw.Spec.LeaseName)
	}
	return *lease.Spec.HolderIdentity, nil
}

func (r *EgressGatewayReconciler) findCurrentGatewayNode(ctx context.Context, egw *egressv1alpha1.EgressGateway) (string, error) {
	var nodeList corev1.NodeList
	if err := r.List(ctx, &nodeList); err != nil {
		return "", fmt.Errorf("list nodes: %w", err)
	}
	labelKey := egw.Spec.NodeLabelKey
	labelVal := labelValue(egw)
	for i := range nodeList.Items {
		v, ok := nodeList.Items[i].Labels[labelKey]
		if ok && v == labelVal {
			return nodeList.Items[i].Name, nil
		}
	}
	return "", nil
}

func (r *EgressGatewayReconciler) switchGateway(ctx context.Context, egw *egressv1alpha1.EgressGateway, currentNode, desiredNode string) error {
	labelKey := egw.Spec.NodeLabelKey
	labelVal := labelValue(egw)
	logger := log.FromContext(ctx)

	if currentNode != "" {
		var oldNode corev1.Node
		if err := r.Get(ctx, types.NamespacedName{Name: currentNode}, &oldNode); err != nil {
			if !errors.IsNotFound(err) {
				return fmt.Errorf("get old gateway node %q: %w", currentNode, err)
			}
			logger.Info("old gateway node not found, skipping label removal", "node", currentNode)
		} else {
			patch := client.MergeFrom(oldNode.DeepCopy())
			delete(oldNode.Labels, labelKey)
			if err := r.Patch(ctx, &oldNode, patch); err != nil {
				return fmt.Errorf("remove label from node %q: %w", currentNode, err)
			}
			logger.Info("removed label from old gateway node", "node", currentNode, "labelKey", labelKey)
		}
	}

	var newNode corev1.Node
	if err := r.Get(ctx, types.NamespacedName{Name: desiredNode}, &newNode); err != nil {
		return fmt.Errorf("get new gateway node %q: %w", desiredNode, err)
	}
	if newNode.Labels == nil {
		newNode.Labels = make(map[string]string)
	}
	if newNode.Labels[labelKey] == labelVal {
		logger.Info("label already set on new gateway node, skipping", "node", desiredNode, "labelKey", labelKey, "labelValue", labelVal)
		return nil
	}
	patch := client.MergeFrom(newNode.DeepCopy())
	newNode.Labels[labelKey] = labelVal
	if err := r.Patch(ctx, &newNode, patch); err != nil {
		return fmt.Errorf("add label to node %q: %w", desiredNode, err)
	}
	logger.Info("added label to new gateway node", "node", desiredNode, "labelKey", labelKey, "labelValue", labelVal)

	return nil
}

func (r *EgressGatewayReconciler) debounceDuration(egw *egressv1alpha1.EgressGateway) time.Duration {
	if egw.Spec.DebounceDuration != nil {
		return egw.Spec.DebounceDuration.Duration
	}
	return 0
}

func (r *EgressGatewayReconciler) requeueInterval(egw *egressv1alpha1.EgressGateway) time.Duration {
	if egw.Spec.RequeueInterval != nil {
		return egw.Spec.RequeueInterval.Duration
	}
	return 5 * time.Second
}

func (r *EgressGatewayReconciler) shouldDebounce(egw *egressv1alpha1.EgressGateway, desiredNode string, debounce time.Duration) bool {
	if egw.Status.DesiredGatewayNode != desiredNode {
		return true
	}
	if egw.Status.DesiredSince == nil {
		return true
	}
	elapsed := time.Since(egw.Status.DesiredSince.Time)
	return elapsed < debounce
}

func (r *EgressGatewayReconciler) remainingDebounce(egw *egressv1alpha1.EgressGateway, desiredNode string, debounce time.Duration) time.Duration {
	if egw.Status.DesiredGatewayNode != desiredNode || egw.Status.DesiredSince == nil {
		return debounce
	}
	elapsed := time.Since(egw.Status.DesiredSince.Time)
	remaining := debounce - elapsed
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (r *EgressGatewayReconciler) setDesiredInStatus(ctx context.Context, egw *egressv1alpha1.EgressGateway, desiredNode string) error {
	if egw.Status.DesiredGatewayNode == desiredNode && egw.Status.DesiredSince != nil {
		return nil
	}
	now := metav1.Now()
	egw.Status.DesiredGatewayNode = desiredNode
	egw.Status.DesiredSince = &now
	egw.Status.ObservedGeneration = egw.Generation
	return r.Status().Update(ctx, egw)
}

func (r *EgressGatewayReconciler) updateStatus(ctx context.Context, egw *egressv1alpha1.EgressGateway, currentNode string) error {
	if egw.Status.CurrentGatewayNode == currentNode && egw.Status.ObservedGeneration == egw.Generation {
		return nil
	}
	egw.Status.CurrentGatewayNode = currentNode
	egw.Status.DesiredGatewayNode = currentNode
	egw.Status.ObservedGeneration = egw.Generation
	return r.Status().Update(ctx, egw)
}

func labelValue(egw *egressv1alpha1.EgressGateway) string {
	if egw.Spec.NodeLabelValue != "" {
		return egw.Spec.NodeLabelValue
	}
	return "true"
}

func SetupWithManager(mgr manager.Manager) error {
	r := &EgressGatewayReconciler{
		Client:   mgr.GetClient(),
		Selector: gateway.NewSelector(mgr.GetClient()),
	}

	leaseToGateway := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		var egwList egressv1alpha1.EgressGatewayList
		if err := mgr.GetClient().List(ctx, &egwList); err != nil {
			return nil
		}
		var requests []reconcile.Request
		for i := range egwList.Items {
			egw := &egwList.Items[i]
			if egw.Spec.LeaseName == obj.GetName() && egw.Spec.LeaseNamespace == obj.GetNamespace() {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: egw.Name},
				})
			}
		}
		return requests
	})

	nodeToGateway := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		var egwList egressv1alpha1.EgressGatewayList
		if err := mgr.GetClient().List(ctx, &egwList); err != nil {
			return nil
		}
		requests := make([]reconcile.Request, 0, len(egwList.Items))
		for i := range egwList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: egwList.Items[i].Name},
			})
		}
		return requests
	})

	leasePredicate := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldLease, okOld := e.ObjectOld.(*coordinationv1.Lease)
			newLease, okNew := e.ObjectNew.(*coordinationv1.Lease)
			if !okOld || !okNew {
				return true
			}
			oldHolder := ""
			if oldLease.Spec.HolderIdentity != nil {
				oldHolder = *oldLease.Spec.HolderIdentity
			}
			newHolder := ""
			if newLease.Spec.HolderIdentity != nil {
				newHolder = *newLease.Spec.HolderIdentity
			}
			if oldHolder != newHolder && newHolder != "" {
				metrics.IncLeaderChange()
				ctrl.Log.Info("lease leader changed", "lease", newLease.Namespace+"/"+newLease.Name, "oldHolder", oldHolder, "newHolder", newHolder)
				return true
			}
			return false
		},
	}

	nodePredicate := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldNode, okOld := e.ObjectOld.(*corev1.Node)
			newNode, okNew := e.ObjectNew.(*corev1.Node)
			if !okOld || !okNew {
				return true
			}
			oldReady := false
			newReady := false
			for i := range oldNode.Status.Conditions {
				if oldNode.Status.Conditions[i].Type == corev1.NodeReady {
					oldReady = oldNode.Status.Conditions[i].Status == corev1.ConditionTrue
				}
			}
			for i := range newNode.Status.Conditions {
				if newNode.Status.Conditions[i].Type == corev1.NodeReady {
					newReady = newNode.Status.Conditions[i].Status == corev1.ConditionTrue
				}
			}
			return oldReady != newReady
		},
	}

	return builder.ControllerManagedBy(mgr).
		Named("egress-gateway-controller").
		For(&egressv1alpha1.EgressGateway{}).
		Watches(&coordinationv1.Lease{}, leaseToGateway, builder.WithPredicates(leasePredicate)).
		Watches(&corev1.Node{}, nodeToGateway, builder.WithPredicates(nodePredicate)).
		Complete(r)
}
