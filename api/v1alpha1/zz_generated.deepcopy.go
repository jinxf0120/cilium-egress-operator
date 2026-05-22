package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func (in *EgressGateway) DeepCopyInto(out *EgressGateway) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *EgressGateway) DeepCopy() *EgressGateway {
	if in == nil {
		return nil
	}
	out := new(EgressGateway)
	in.DeepCopyInto(out)
	return out
}

func (in *EgressGateway) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *EgressGatewayList) DeepCopyInto(out *EgressGatewayList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]EgressGateway, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

func (in *EgressGatewayList) DeepCopy() *EgressGatewayList {
	if in == nil {
		return nil
	}
	out := new(EgressGatewayList)
	in.DeepCopyInto(out)
	return out
}

func (in *EgressGatewayList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *EgressGatewaySpec) DeepCopyInto(out *EgressGatewaySpec) {
	*out = *in
	if in.Candidates != nil {
		in, out := &in.Candidates, &out.Candidates
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.DebounceDuration != nil {
		in, out := &in.DebounceDuration, &out.DebounceDuration
		*out = new(metav1.Duration)
		**out = **in
	}
	if in.RequeueInterval != nil {
		in, out := &in.RequeueInterval, &out.RequeueInterval
		*out = new(metav1.Duration)
		**out = **in
	}
}

func (in *EgressGatewaySpec) DeepCopy() *EgressGatewaySpec {
	if in == nil {
		return nil
	}
	out := new(EgressGatewaySpec)
	in.DeepCopyInto(out)
	return out
}

func (in *EgressGatewayStatus) DeepCopyInto(out *EgressGatewayStatus) {
	*out = *in
	if in.DesiredSince != nil {
		in, out := &in.DesiredSince, &out.DesiredSince
		*out = (*in).DeepCopy()
	}
	if in.LastSwitchTime != nil {
		in, out := &in.LastSwitchTime, &out.LastSwitchTime
		*out = (*in).DeepCopy()
	}
}

func (in *EgressGatewayStatus) DeepCopy() *EgressGatewayStatus {
	if in == nil {
		return nil
	}
	out := new(EgressGatewayStatus)
	in.DeepCopyInto(out)
	return out
}
