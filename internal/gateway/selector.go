package gateway

import (
	"context"
	"fmt"

	egressv1alpha1 "github.com/jinxf0120/cilium-egress-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Selector struct {
	k8sClient client.Client
}

func NewSelector(k8sClient client.Client) *Selector {
	return &Selector{k8sClient: k8sClient}
}

func (s *Selector) Select(ctx context.Context, egw *egressv1alpha1.EgressGateway, leaderIdentity string) (string, error) {
	nodeName, err := s.resolveLeader(ctx, leaderIdentity)
	if err != nil {
		return "", err
	}

	if len(egw.Spec.Candidates) > 0 {
		return s.applyCandidateFilter(egw, nodeName)
	}

	return nodeName, nil
}

func (s *Selector) resolveLeader(ctx context.Context, leaderIdentity string) (string, error) {
	var node corev1.Node
	if err := s.k8sClient.Get(ctx, types.NamespacedName{Name: leaderIdentity}, &node); err == nil {
		return node.Name, nil
	}

	var nodeList corev1.NodeList
	if err := s.k8sClient.List(ctx, &nodeList); err != nil {
		return "", fmt.Errorf("list nodes: %w", err)
	}
	for i := range nodeList.Items {
		for _, addr := range nodeList.Items[i].Status.Addresses {
			if addr.Type == corev1.NodeHostName && addr.Address == leaderIdentity {
				return nodeList.Items[i].Name, nil
			}
		}
	}

	return "", fmt.Errorf("no node matching identity %q (tried node name and hostname)", leaderIdentity)
}

func (s *Selector) applyCandidateFilter(egw *egressv1alpha1.EgressGateway, nodeName string) (string, error) {
	for _, c := range egw.Spec.Candidates {
		if c == nodeName {
			return nodeName, nil
		}
	}
	if egw.Spec.FallbackCandidate != "" {
		return egw.Spec.FallbackCandidate, nil
	}
	return "", fmt.Errorf("node %q is not a candidate and no fallback defined", nodeName)
}

func IsNodeReady(node *corev1.Node) bool {
	for i := range node.Status.Conditions {
		c := &node.Status.Conditions[i]
		if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
