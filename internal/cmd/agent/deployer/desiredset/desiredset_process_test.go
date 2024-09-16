package desiredset

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
)

func Test_multiNamespaceList(t *testing.T) {
	results := map[string]*unstructured.UnstructuredList{
		"ns1": {Items: []unstructured.Unstructured{
			{Object: map[string]interface{}{"name": "o1", "namespace": "ns1"}},
			{Object: map[string]interface{}{"name": "o2", "namespace": "ns1"}},
			{Object: map[string]interface{}{"name": "o3", "namespace": "ns1"}},
		}},
		"ns2": {Items: []unstructured.Unstructured{
			{Object: map[string]interface{}{"name": "o4", "namespace": "ns2"}},
			{Object: map[string]interface{}{"name": "o5", "namespace": "ns2"}},
		}},
		"ns3": {Items: []unstructured.Unstructured{}},
	}
	s := runtime.NewScheme()
	err := appsv1.SchemeBuilder.AddToScheme(s)
	assert.NoError(t, err, "Failed to build schema.")
	baseClient := fake.NewSimpleDynamicClient(s)
	baseClient.PrependReactor("list", "*", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		if strings.Contains(action.GetNamespace(), "error") {
			return true, nil, errors.New("simulated failure")
		}

		return true, results[action.GetNamespace()], nil
	})

	type args struct {
		namespaces []string
	}
	tests := []struct {
		name          string
		args          args
		expectedCalls int
		expectError   bool
	}{
		{
			name: "no namespaces",
			args: args{
				namespaces: []string{},
			},
			expectError:   false,
			expectedCalls: 0,
		},
		{
			name: "1 namespace",
			args: args{
				namespaces: []string{"ns1"},
			},
			expectError:   false,
			expectedCalls: 3,
		},
		{
			name: "many namespaces",
			args: args{
				namespaces: []string{"ns1", "ns2", "ns3"},
			},
			expectError:   false,
			expectedCalls: 5,
		},
		{
			name: "1 namespace error",
			args: args{
				namespaces: []string{"error", "ns2", "ns3"},
			},
			expectError:   true,
			expectedCalls: -1,
		},
		{
			name: "many namespace errors",
			args: args{
				namespaces: []string{"error", "error1", "error2"},
			},
			expectError:   true,
			expectedCalls: -1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls int
			err := multiNamespaceList(context.TODO(), tt.args.namespaces, baseClient.Resource(appsv1.SchemeGroupVersion.WithResource("deployments")), labels.NewSelector(), func(obj unstructured.Unstructured) {
				calls += 1
			})

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tt.expectedCalls >= 0 {
				assert.Equal(t, tt.expectedCalls, calls)
			}
		})
	}
}

func Test_getIndexableHash(t *testing.T) {
	const hash = "somehash"
	hashSelector, err := getSelector(map[string]string{LabelHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	envLabelSelector, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{MatchLabels: map[string]string{"env": "dev"}})
	if err != nil {
		t.Fatal(err)
	}

	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{byHash: func(obj interface{}) ([]string, error) {
		return nil, nil
	}})
	type args struct {
		indexer  cache.Indexer
		selector labels.Selector
	}
	tests := []struct {
		name     string
		args     args
		wantHash string
		want     bool
	}{
		{name: "indexer configured", args: args{
			indexer:  indexer,
			selector: hashSelector,
		}, wantHash: hash, want: true},
		{name: "indexer not configured", args: args{
			indexer:  cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{}),
			selector: hashSelector,
		}, wantHash: "", want: false},
		{name: "using Everything selector", args: args{
			indexer:  indexer,
			selector: labels.Everything(),
		}, wantHash: "", want: false},
		{name: "using other label selectors", args: args{
			indexer:  indexer,
			selector: envLabelSelector,
		}, wantHash: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotHash, got := getIndexableHash(tt.args.indexer, tt.args.selector)
			assert.Equalf(t, tt.wantHash, gotHash, "getIndexableHash(%v, %v)", tt.args.indexer, tt.args.selector)
			assert.Equalf(t, tt.want, got, "getIndexableHash(%v, %v)", tt.args.indexer, tt.args.selector)
		})
	}
}

func Test_inNamespace(t *testing.T) {
	type args struct {
		namespace string
		obj       interface{}
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{name: "object in namespace", args: args{
			namespace: "ns", obj: &metav1.ObjectMeta{
				Namespace: "ns",
			},
		}, want: true},
		{name: "object not in namespace", args: args{
			namespace: "ns", obj: &metav1.ObjectMeta{
				Namespace: "another-ns",
			},
		}, want: false},
		{name: "object not namespaced", args: args{
			namespace: "ns", obj: &corev1.Namespace{},
		}, want: false},
		{name: "non k8s object", args: args{
			namespace: "ns", obj: &struct{}{},
		}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, inNamespace(tt.args.namespace, tt.args.obj), "inNamespace(%v, %v)", tt.args.namespace, tt.args.obj)
		})
	}
}
