package desiredset

import (
	"context"
	"crypto/sha1" // nolint:gosec // non crypto usage
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/objectset"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/util/flowcontrol"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	LabelApplied   = "objectset.rio.cattle.io/applied"
	LabelID        = "objectset.rio.cattle.io/id"
	LabelGVK       = "objectset.rio.cattle.io/owner-gvk"
	LabelName      = "objectset.rio.cattle.io/owner-name"
	LabelNamespace = "objectset.rio.cattle.io/owner-namespace"
	LabelHash      = "objectset.rio.cattle.io/hash"
	LabelPrefix    = "objectset.rio.cattle.io/"
	LabelPrune     = "objectset.rio.cattle.io/prune"
)

var (
	hashOrder = []string{
		LabelID,
		LabelGVK,
		LabelName,
		LabelNamespace,
	}
	rls     = map[string]flowcontrol.RateLimiter{}
	rlsLock sync.Mutex
)

func (o *desiredSet) getRateLimit(labelHash string) flowcontrol.RateLimiter {
	var rl flowcontrol.RateLimiter

	rlsLock.Lock()
	defer rlsLock.Unlock()
	if o.remove {
		delete(rls, labelHash)
	} else {
		rl = rls[labelHash]
		if rl == nil {
			rl = flowcontrol.NewTokenBucketRateLimiter(o.ratelimitingQps, 10)
			rls[labelHash] = rl
		}
	}

	return rl
}

func (o *desiredSet) apply(ctx context.Context) error {
	logger := log.FromContext(ctx)
	logger.Info("[DEBUG] call to apply")

	if o.objs == nil || o.objs.Len() == 0 {
		o.remove = true
	}

	if err := o.Err(); err != nil {
		return err
	}

	labelSet, annotationSet, err := GetLabelsAndAnnotations(o.setID)
	if err != nil {
		return o.addErr(err)
	}

	rl := o.getRateLimit(labelSet[LabelHash])
	if rl != nil {
		t := time.Now()
		rl.Accept()
		if d := time.Since(t); d.Seconds() > 1 {
			logger := log.FromContext(ctx)
			logger.Info("rate limited", "setID", o.setID, "labels", labelSet, "duration", d)
		}
	}

	objList, err := o.injectLabelsAndAnnotations(labelSet, annotationSet)
	if err != nil {
		return o.addErr(err)
	}

	objs := o.collect(objList)

	sel, err := getSelector(labelSet)
	if err != nil {
		return o.addErr(err)
	}

	for _, gvk := range o.objs.GVKOrder(o.knownGVK()...) {
		logger := log.FromContext(ctx)
		logger.Info("calling process", "selector", sel, "gvk", gvk)
		o.process(ctx, sel, gvk, objs[gvk])
	}

	logger.Info("[DEBUG] error after call to process?", "err", o.Err())

	return o.Err()
}

func (o *desiredSet) knownGVK() (ret []schema.GroupVersionKind) {
	return
}

func (o *desiredSet) collect(objList []runtime.Object) objectset.ObjectByGVK {
	result := objectset.ObjectByGVK{}
	for _, obj := range objList {
		_, _ = result.Add(obj)
	}
	return result
}

func getSelector(labelSet map[string]string) (labels.Selector, error) {
	req, err := labels.NewRequirement(LabelHash, selection.Equals, []string{labelSet[LabelHash]})
	if err != nil {
		return nil, err
	}
	return labels.NewSelector().Add(*req), nil
}

func (o *desiredSet) injectLabelsAndAnnotations(labels, annotations map[string]string) ([]runtime.Object, error) {
	var result []runtime.Object

	for _, objMap := range o.objs.ObjectsByGVK() {
		for key, obj := range objMap {
			obj = obj.DeepCopyObject()
			meta, err := meta.Accessor(obj)
			if err != nil {
				return nil, fmt.Errorf("failed to get metadata for %s: %w", key, err)
			}

			setLabels(meta, labels)
			setAnnotations(meta, annotations)

			result = append(result, obj)
		}
	}

	return result, nil
}

func setAnnotations(meta metav1.Object, annotations map[string]string) {
	objAnn := meta.GetAnnotations()
	if objAnn == nil {
		objAnn = map[string]string{}
	}
	delete(objAnn, LabelApplied)
	for k, v := range annotations {
		objAnn[k] = v
	}
	meta.SetAnnotations(objAnn)
}

func setLabels(meta metav1.Object, labels map[string]string) {
	objLabels := meta.GetLabels()
	if objLabels == nil {
		objLabels = map[string]string{}
	}
	for k, v := range labels {
		objLabels[k] = v
	}
	meta.SetLabels(objLabels)
}

func objectSetHash(labels map[string]string) string {
	dig := sha1.New() // nolint:gosec // non crypto usage
	for _, key := range hashOrder {
		dig.Write([]byte(labels[key]))
	}
	return hex.EncodeToString(dig.Sum(nil))
}
