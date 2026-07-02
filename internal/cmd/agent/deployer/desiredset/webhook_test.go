package desiredset

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestNormalizeWebhookCABundlePatch_CertManager_MutatingWebhook(t *testing.T) {
	realPatch := []byte(`{"webhooks":[{"admissionReviewVersions":["v1"],"clientConfig":{"caBundle":"LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUJ4VENDQVV1Z0F3SUJBZ0lVVCtBTTR4RnE0emJxTGtHOHNkcTlzOWN5SVpRd0NnWUlLb1pJemowRUF3TXcKSWpFZ01CNEdBMVVFQXhNWFkyVnlkQzF0WVc1aFoyVnlMWGRsWW1odmIyc3RZMkV3SGhjTk1qWXdOakkyTWpBdwpPVEl4V2hjTk1qY3dOakkyTWpBd09USXhXakFpTVNBd0hnWURWUVFERXhkalpYSjBMVzFoYm1GblpYSXRkMlZpCmFHOXZheTFqWVRCMk1CQUdCeXFHU000OUFnRUdCU3VCQkFBaUEySUFCRW50Q3dWSkVUdTlSbm14ZEFCQ2J0ODEKNGZZMjhMZ3dURzJOSEFlR0oxR1kwaWx2MHRGU0ZiaUphYWlIcmdTWG40dUdObUhNV2Q3YTFWc0kzT2ZXejJzRQpUaEYrLzdnMU1oUzVUSU1xalg2Z1F3U2dqNE5wRjZ3ZmVoMG5Lb1Y2Q2FOQ01FQXdEZ1lEVlIwUEFRSC9CQVFECkFnS2tNQThHQTFVZEV3RUIvd1FGTUFNQkFmOHdIUVlEVlIwT0JCWUVGT1l2QVpyeTh2UEVWbitLZkhsdGc0ckEKZmJJSk1Bb0dDQ3FHU000OUJBTURBMmdBTUdVQ01RQ0RWRW5SQ1JoSGVMRGorUG8zT1U4cU1YTjdqWU1SOHV4SAorWGZJM0xITzVxVlFZa1orT3dFaEljaFQxd1ZCQ25JQ01GbTloc05hWmZBaiswcW9jTEkzQU9rYU1KUEpVWWJKCnJiL2U3ZXdmZVZwUFhwVEVXWlk4WDZSUzNqMy90WnRyTVE9PQotLS0tLUVORCBDRVJUSUZJQ0FURS0tLS0tCg==","service":{"name":"cert-manager-webhook","namespace":"cert-manager","path":"/mutate","port":443}},"failurePolicy":"Fail","matchPolicy":"Equivalent","name":"webhook.cert-manager.io","namespaceSelector":{},"objectSelector":{},"reinvocationPolicy":"Never","rules":[{"apiGroups":["cert-manager.io"],"apiVersions":["v1"],"operations":["CREATE"],"resources":["certificaterequests"]}],"sideEffects":"None","timeoutSeconds":30}]}`)

	desired := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "admissionregistration.k8s.io/v1",
			"kind":       "MutatingWebhookConfiguration",
			"metadata": map[string]any{
				"name": "cert-manager-webhook",
			},
			"webhooks": []any{
				map[string]any{
					"name":                    "webhook.cert-manager.io",
					"admissionReviewVersions": []any{"v1"},
					"clientConfig": map[string]any{
						"service": map[string]any{
							"name":      "cert-manager-webhook",
							"namespace": "cert-manager",
							"path":      "/mutate",
						},
					},
					"rules": []any{
						map[string]any{
							"apiGroups":   []any{"cert-manager.io"},
							"apiVersions": []any{"v1"},
							"operations":  []any{"CREATE"},
							"resources":   []any{"certificaterequests"},
						},
					},
					"sideEffects": "None",
				},
			},
		},
	}

	actual := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "admissionregistration.k8s.io/v1",
			"kind":       "MutatingWebhookConfiguration",
			"metadata": map[string]any{
				"name": "cert-manager-webhook",
				"uid":  "fake-uid-12345",
			},
			"webhooks": []any{
				map[string]any{
					"name":                    "webhook.cert-manager.io",
					"admissionReviewVersions": []any{"v1"},
					"clientConfig": map[string]any{
						"service": map[string]any{
							"name":      "cert-manager-webhook",
							"namespace": "cert-manager",
							"path":      "/mutate",
							"port":      443, // API default
						},
						"caBundle": "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUJ4VENDQVV1Z0F3SUJBZ0lVVCtBTTR4RnE0emJxTGtHOHNkcTlzOWN5SVpRd0NnWUlLb1pJemowRUF3TXcKSWpFZ01CNEdBMVVFQXhNWFkyVnlkQzF0WVc1aFoyVnlMWGRsWW1odmIyc3RZMkV3SGhjTk1qWXdOakkyTWpBdwpPVEl4V2hjTk1qY3dOakkyTWpBd09USXhXakFpTVNBd0hnWURWUVFERXhkalpYSjBMVzFoYm1GblpYSXRkMlZpCmFHOXZheTFqWVRCMk1CQUdCeXFHU000OUFnRUdCU3VCQkFBaUEySUFCRW50Q3dWSkVUdTlSbm14ZEFCQ2J0ODEKNGZZMjhMZ3dURzJOSEFlR0oxR1kwaWx2MHRGU0ZiaUphYWlIcmdTWG40dUdObUhNV2Q3YTFWc0kzT2ZXejJzRQpUaEYrLzdnMU1oUzVUSU1xalg2Z1F3U2dqNE5wRjZ3ZmVoMG5Lb1Y2Q2FOQ01FQXdEZ1lEVlIwUEFRSC9CQVFECkFnS2tNQThHQTFVZEV3RUIvd1FGTUFNQkFmOHdIUVlEVlIwT0JCWUVGT1l2QVpyeTh2UEVWbitLZkhsdGc0ckEKZmJJSk1Bb0dDQ3FHU000OUJBTURBMmdBTUdVQ01RQ0RWRW5SQ1JoSGVMRGorUG8zT1U4cU1YTjdqWU1SOHV4SAorWGZJM0xITzVxVlFZa1orT3dFaEljaFQxd1ZCQ25JQ01GbTloc05hWmZBaiswcW9jTEkzQU9rYU1KUEpVWWJKCnJiL2U3ZXdmZVZwUFhwVEVXWlk4WDZSUzNqMy90WnRyTVE9PQotLS0tLUVORCBDRVJUSUZJQ0FURS0tLS0tCg==", // Controller-injected
					},
					"failurePolicy":      "Fail",           // API default
					"matchPolicy":        "Equivalent",     // API default
					"namespaceSelector":  map[string]any{}, // API default
					"objectSelector":     map[string]any{}, // API default
					"reinvocationPolicy": "Never",          // API default
					"rules": []any{
						map[string]any{
							"apiGroups":   []any{"cert-manager.io"},
							"apiVersions": []any{"v1"},
							"operations":  []any{"CREATE"},
							"resources":   []any{"certificaterequests"},
						},
					},
					"sideEffects":    "None",
					"timeoutSeconds": 30, // API default
				},
			},
		},
	}

	patch := realPatch
	_, err := normalizeWebhookCABundlePatch(desired, actual, &patch)
	if err != nil {
		t.Fatalf("normalizeWebhookCABundlePatch failed: %v", err)
	}

	if strings.Contains(string(patch), "caBundle") {
		t.Errorf("Expected caBundle to be removed from patch, but it's still present: %s", string(patch))
	}

	if string(patch) == string(realPatch) {
		t.Error("Expected patch to be modified, but it's unchanged")
	}

	t.Logf("Normalized patch (caBundle removed): %s", string(patch))
}

func TestNormalizeWebhookCABundlePatch_CertManager_ValidatingWebhook(t *testing.T) {
	realPatch := []byte(`{"webhooks":[{"admissionReviewVersions":["v1"],"clientConfig":{"caBundle":"LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUJ4VENDQVV1Z0F3SUJBZ0lVVCtBTTR4RnE0emJxTGtHOHNkcTlzOWN5SVpRd0NnWUlLb1pJemowRUF3TXcKSWpFZ01CNEdBMVVFQXhNWFkyVnlkQzF0WVc1aFoyVnlMWGRsWW1odmIyc3RZMkV3SGhjTk1qWXdOakkyTWpBdwpPVEl4V2hjTk1qY3dOakkyTWpBd09USXhXakFpTVNBd0hnWURWUVFERXhkalpYSjBMVzFoYm1GblpYSXRkMlZpCmFHOXZheTFqWVRCMk1CQUdCeXFHU000OUFnRUdCU3VCQkFBaUEySUFCRW50Q3dWSkVUdTlSbm14ZEFCQ2J0ODEKNGZZMjhMZ3dURzJOSEFlR0oxR1kwaWx2MHRGU0ZiaUphYWlIcmdTWG40dUdObUhNV2Q3YTFWc0kzT2ZXejJzRQpUaEYrLzdnMU1oUzVUSU1xalg2Z1F3U2dqNE5wRjZ3ZmVoMG5Lb1Y2Q2FOQ01FQXdEZ1lEVlIwUEFRSC9CQVFECkFnS2tNQThHQTFVZEV3RUIvd1FGTUFNQkFmOHdIUVlEVlIwT0JCWUVGT1l2QVpyeTh2UEVWbitLZkhsdGc0ckEKZmJJSk1Bb0dDQ3FHU000OUJBTURBMmdBTUdVQ01RQ0RWRW5SQ1JoSGVMRGorUG8zT1U4cU1YTjdqWU1SOHV4SAorWGZJM0xITzVxVlFZa1orT3dFaEljaFQxd1ZCQ25JQ01GbTloc05hWmZBaiswcW9jTEkzQU9rYU1KUEpVWWJKCnJiL2U3ZXdmZVZwUFhwVEVXWlk4WDZSUzNqMy90WnRyTVE9PQotLS0tLUVORCBDRVJUSUZJQ0FURS0tLS0tCg==","service":{"name":"cert-manager-webhook","namespace":"cert-manager","path":"/validate","port":443}},"failurePolicy":"Fail","matchPolicy":"Equivalent","name":"webhook.cert-manager.io","namespaceSelector":{"matchExpressions":[{"key":"cert-manager.io/disable-validation","operator":"NotIn","values":["true"]}]},"objectSelector":{},"rules":[{"apiGroups":["cert-manager.io","acme.cert-manager.io"],"apiVersions":["v1"],"operations":["CREATE","UPDATE"],"resources":["*/*"]}],"sideEffects":"None","timeoutSeconds":30}]}`)

	desired := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "admissionregistration.k8s.io/v1",
			"kind":       "ValidatingWebhookConfiguration",
			"metadata": map[string]any{
				"name": "cert-manager-webhook",
			},
			"webhooks": []any{
				map[string]any{
					"name":                    "webhook.cert-manager.io",
					"admissionReviewVersions": []any{"v1"},
					"clientConfig": map[string]any{
						"service": map[string]any{
							"name":      "cert-manager-webhook",
							"namespace": "cert-manager",
							"path":      "/validate",
						},
					},
					"namespaceSelector": map[string]any{
						"matchExpressions": []any{
							map[string]any{
								"key":      "cert-manager.io/disable-validation",
								"operator": "NotIn",
								"values":   []any{"true"},
							},
						},
					},
					"rules": []any{
						map[string]any{
							"apiGroups":   []any{"cert-manager.io", "acme.cert-manager.io"},
							"apiVersions": []any{"v1"},
							"operations":  []any{"CREATE", "UPDATE"},
							"resources":   []any{"*/*"},
						},
					},
					"sideEffects": "None",
				},
			},
		},
	}

	actual := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "admissionregistration.k8s.io/v1",
			"kind":       "ValidatingWebhookConfiguration",
			"metadata": map[string]any{
				"name": "cert-manager-webhook",
				"uid":  "fake-uid-67890",
			},
			"webhooks": []any{
				map[string]any{
					"name":                    "webhook.cert-manager.io",
					"admissionReviewVersions": []any{"v1"},
					"clientConfig": map[string]any{
						"service": map[string]any{
							"name":      "cert-manager-webhook",
							"namespace": "cert-manager",
							"path":      "/validate",
							"port":      443,
						},
						"caBundle": "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUJ4VENDQVV1Z0F3SUJBZ0lVVCtBTTR4RnE0emJxTGtHOHNkcTlzOWN5SVpRd0NnWUlLb1pJemowRUF3TXcKSWpFZ01CNEdBMVVFQXhNWFkyVnlkQzF0WVc1aFoyVnlMWGRsWW1odmIyc3RZMkV3SGhjTk1qWXdOakkyTWpBdwpPVEl4V2hjTk1qY3dOakkyTWpBd09USXhXakFpTVNBd0hnWURWUVFERXhkalpYSjBMVzFoYm1GblpYSXRkMlZpCmFHOXZheTFqWVRCMk1CQUdCeXFHU000OUFnRUdCU3VCQkFBaUEySUFCRW50Q3dWSkVUdTlSbm14ZEFCQ2J0ODEKNGZZMjhMZ3dURzJOSEFlR0oxR1kwaWx2MHRGU0ZiaUphYWlIcmdTWG40dUdObUhNV2Q3YTFWc0kzT2ZXejJzRQpUaEYrLzdnMU1oUzVUSU1xalg2Z1F3U2dqNE5wRjZ3ZmVoMG5Lb1Y2Q2FOQ01FQXdEZ1lEVlIwUEFRSC9CQVFECkFnS2tNQThHQTFVZEV3RUIvd1FGTUFNQkFmOHdIUVlEVlIwT0JCWUVGT1l2QVpyeTh2UEVWbitLZkhsdGc0ckEKZmJJSk1Bb0dDQ3FHU000OUJBTURBMmdBTUdVQ01RQ0RWRW5SQ1JoSGVMRGorUG8zT1U4cU1YTjdqWU1SOHV4SAorWGZJM0xITzVxVlFZa1orT3dFaEljaFQxd1ZCQ25JQ01GbTloc05hWmZBaiswcW9jTEkzQU9rYU1KUEpVWWJKCnJiL2U3ZXdmZVZwUFhwVEVXWlk4WDZSUzNqMy90WnRyTVE9PQotLS0tLUVORCBDRVJUSUZJQ0FURS0tLS0tCg==",
					},
					"failurePolicy": "Fail",
					"matchPolicy":   "Equivalent",
					"namespaceSelector": map[string]any{
						"matchExpressions": []any{
							map[string]any{
								"key":      "cert-manager.io/disable-validation",
								"operator": "NotIn",
								"values":   []any{"true"},
							},
						},
					},
					"objectSelector": map[string]any{},
					"rules": []any{
						map[string]any{
							"apiGroups":   []any{"cert-manager.io", "acme.cert-manager.io"},
							"apiVersions": []any{"v1"},
							"operations":  []any{"CREATE", "UPDATE"},
							"resources":   []any{"*/*"},
						},
					},
					"sideEffects":    "None",
					"timeoutSeconds": 30,
				},
			},
		},
	}

	patch := realPatch
	_, err := normalizeWebhookCABundlePatch(desired, actual, &patch)
	if err != nil {
		t.Fatalf("normalizeWebhookCABundlePatch failed: %v", err)
	}

	// Verify caBundle is removed
	if strings.Contains(string(patch), "caBundle") {
		t.Errorf("Expected caBundle to be removed from patch, but it's still present: %s", string(patch))
	}

	if string(patch) == string(realPatch) {
		t.Error("Expected patch to be modified, but it's unchanged")
	}

	t.Logf("Normalized patch (caBundle removed): %s", string(patch))
}

func TestNormalizeWebhookCABundlePatch_UserManagedCABundle(t *testing.T) {
	patch := []byte(`{"webhooks":[{"clientConfig":{"caBundle":"VVNFUl9NQU5BR0VEX0NBX0JVTkRMRQ==","service":{"name":"my-webhook","namespace":"default"}},"name":"validate.example.com"}]}`)

	desired := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "admissionregistration.k8s.io/v1",
			"kind":       "ValidatingWebhookConfiguration",
			"metadata": map[string]any{
				"name": "my-webhook",
			},
			"webhooks": []any{
				map[string]any{
					"name": "validate.example.com",
					"clientConfig": map[string]any{
						"service": map[string]any{
							"name":      "my-webhook",
							"namespace": "default",
						},
						"caBundle": "VVNFUl9NQU5BR0VEX0NBX0JVTkRMRQ==", // User-managed
					},
				},
			},
		},
	}

	actual := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "admissionregistration.k8s.io/v1",
			"kind":       "ValidatingWebhookConfiguration",
			"metadata": map[string]any{
				"name": "my-webhook",
			},
			"webhooks": []any{
				map[string]any{
					"name": "validate.example.com",
					"clientConfig": map[string]any{
						"service": map[string]any{
							"name":      "my-webhook",
							"namespace": "default",
						},
						"caBundle": "RElGRkVSRU5UX0NBX0JVTkRMRQ==", // Drifted
					},
				},
			},
		},
	}

	emptied, err := normalizeWebhookCABundlePatch(desired, actual, &patch)
	if err != nil {
		t.Fatalf("normalizeWebhookCABundlePatch failed: %v", err)
	}

	// Patch should NOT be emptied - we want to detect drift in user-managed caBundles
	if emptied {
		t.Error("Expected patch to be preserved (user-managed caBundle drift detection), but it was emptied")
	}

	// Verify the patch still contains caBundle
	if !strings.Contains(string(patch), "caBundle") {
		t.Errorf("Expected patch to still contain caBundle, but got: %s", string(patch))
	}
}

func TestNormalizeWebhookCABundlePatch_NestedPatch(t *testing.T) {
	// Nested patch: only caBundle differs
	patch := []byte(`{"webhooks":[{"clientConfig":{"caBundle":"Q09OVFJPTExFUl9JTkpFQ1RFRA=="}}]}`)

	desired := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "admissionregistration.k8s.io/v1",
			"kind":       "MutatingWebhookConfiguration",
			"metadata": map[string]any{
				"name": "test-webhook",
			},
			"webhooks": []any{
				map[string]any{
					"name": "webhook.example.com",
					"clientConfig": map[string]any{
						"service": map[string]any{
							"name":      "webhook-service",
							"namespace": "default",
						},
						// No caBundle - controller-managed
					},
				},
			},
		},
	}

	actual := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "admissionregistration.k8s.io/v1",
			"kind":       "MutatingWebhookConfiguration",
			"metadata": map[string]any{
				"name": "test-webhook",
			},
			"webhooks": []any{
				map[string]any{
					"name": "webhook.example.com",
					"clientConfig": map[string]any{
						"service": map[string]any{
							"name":      "webhook-service",
							"namespace": "default",
						},
						"caBundle": "Q09OVFJPTExFUl9JTkpFQ1RFRA==",
					},
				},
			},
		},
	}

	emptied, err := normalizeWebhookCABundlePatch(desired, actual, &patch)
	if err != nil {
		t.Fatalf("normalizeWebhookCABundlePatch failed: %v", err)
	}

	// Patch should be emptied since only the controller-injected caBundle differs
	if !emptied {
		t.Errorf("Expected nested patch to be emptied, but got: %s", string(patch))
	}
}

func TestNormalizeWebhookCABundlePatch_NonWebhookResource(t *testing.T) {
	// Patch for a hypothetical CustomResourceDefinition with a caBundle field
	originalPatch := []byte(`{"spec":{"conversion":{"webhook":{"clientConfig":{"caBundle":"U09NRV9DQV9CVU5ETEU=","service":{"name":"webhook-service","namespace":"default"}}}}}}`)

	desired := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata": map[string]any{
				"name": "myresources.example.com",
			},
			"spec": map[string]any{
				"conversion": map[string]any{
					"webhook": map[string]any{
						"clientConfig": map[string]any{
							"service": map[string]any{
								"name":      "webhook-service",
								"namespace": "default",
							},
						},
					},
				},
			},
		},
	}

	actual := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata": map[string]any{
				"name": "myresources.example.com",
			},
			"spec": map[string]any{
				"conversion": map[string]any{
					"webhook": map[string]any{
						"clientConfig": map[string]any{
							"service": map[string]any{
								"name":      "webhook-service",
								"namespace": "default",
							},
							"caBundle": "U09NRV9DQV9CVU5ETEU=",
						},
					},
				},
			},
		},
	}

	patch := originalPatch
	emptied, err := normalizeWebhookCABundlePatch(desired, actual, &patch)
	if err != nil {
		t.Fatalf("normalizeWebhookCABundlePatch failed: %v", err)
	}

	// The patch should be UNCHANGED - this function only handles webhook configurations
	if emptied {
		t.Error("Expected non-webhook resource to be unaffected, but patch was emptied")
	}

	if string(patch) != string(originalPatch) {
		t.Errorf("Expected patch to remain unchanged for non-webhook resource, but it was modified.\nOriginal: %s\nModified: %s", string(originalPatch), string(patch))
	}

	// Verify caBundle is still present
	if !strings.Contains(string(patch), "caBundle") {
		t.Error("Expected caBundle to remain in patch for non-webhook resource, but it was removed")
	}
}
