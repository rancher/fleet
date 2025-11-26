package helmdeployer

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/kube"

	"os"

	"github.com/rancher/fleet/internal/experimental"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestValuesFrom(t *testing.T) {
	a := assert.New(t)
	r := require.New(t)
	key := "values.yaml"
	newline := "\n"
	if runtime.GOOS == "windows" {
		newline = "\r\n"
	}

	configMapPayload := fmt.Sprintf("replication: \"true\"%sreplicas: \"2\"%sserviceType: NodePort", newline, newline)
	secretPayload := fmt.Sprintf("replication: \"false\"%sreplicas: \"3\"%sserviceType: NodePort%sfoo: bar", newline, newline, newline)
	totalValues := map[string]interface{}{"beforeMerge": "value"}
	expected := map[string]interface{}{
		"beforeMerge": "value",
		"replicas":    "2",
		"replication": "true",
		"serviceType": "NodePort",
		"foo":         "bar",
	}

	configMapName := "configmap-name"
	configMapNamespace := "configmap-namespace"
	configMapValues, err := valuesFromConfigMap(configMapName, configMapNamespace, key, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: configMapNamespace,
		},
		Data: map[string]string{
			key: configMapPayload,
		},
	})
	r.NoError(err)

	secretName := "secret-name"
	secretNamespace := "secret-namespace"
	secretValues, err := valuesFromSecret(secretName, secretNamespace, key, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: secretNamespace,
		},
		Data: map[string][]byte{
			key: []byte(secretPayload),
		},
	})
	r.NoError(err)

	totalValues = mergeValues(totalValues, secretValues)
	totalValues = mergeValues(totalValues, configMapValues)
	a.Equal(expected, totalValues)
}

func TestAtomicMapsToRollbackOnFailure(t *testing.T) {
	a := assert.New(t)
	h := &Helm{}

	tests := []struct {
		name     string
		atomic   bool
		expected bool
	}{
		{
			name:     "atomic true maps to RollbackOnFailure true",
			atomic:   true,
			expected: true,
		},
		{
			name:     "atomic false maps to RollbackOnFailure false",
			atomic:   false,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			installAction := &action.Install{}
			h.configureInstallAction(
				installAction,
				&action.Configuration{},
				"test-release",
				"test-namespace",
				time.Duration(0),
				fleet.BundleDeploymentOptions{
					Helm: &fleet.HelmOptions{
						Atomic: tt.atomic,
					},
				},
				nil,
				dryRunConfig{DryRun: false},
			)
			a.Equal(tt.expected, installAction.RollbackOnFailure, "Install: Atomic should map to RollbackOnFailure")

			upgradeAction := &action.Upgrade{}
			h.configureUpgradeAction(
				upgradeAction,
				"test-namespace",
				time.Duration(0),
				fleet.BundleDeploymentOptions{
					Helm: &fleet.HelmOptions{
						Atomic: tt.atomic,
					},
				},
				nil,
				dryRunConfig{DryRun: false},
			)
			a.Equal(tt.expected, upgradeAction.RollbackOnFailure, "Upgrade: Atomic should map to RollbackOnFailure")
		})
	}
}

func TestForceMapsToForceReplace(t *testing.T) {
	a := assert.New(t)
	h := &Helm{}

	tests := []struct {
		name     string
		force    bool
		expected bool
	}{
		{
			name:     "force true maps to ForceReplace true",
			force:    true,
			expected: true,
		},
		{
			name:     "force false maps to ForceReplace false",
			force:    false,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upgradeAction := &action.Upgrade{}
			h.configureUpgradeAction(
				upgradeAction,
				"test-namespace",
				time.Duration(0),
				fleet.BundleDeploymentOptions{
					Helm: &fleet.HelmOptions{
						Force: tt.force,
					},
				},
				nil,
				dryRunConfig{DryRun: false},
			)
			a.Equal(tt.expected, upgradeAction.ForceReplace, "Force should map to ForceReplace")
		})
	}
}

func TestDryRunStrategyMapping(t *testing.T) {
	a := assert.New(t)

	tests := []struct {
		name            string
		template        bool
		dryRun          bool
		dryRunOption    string
		expectedInstall action.DryRunStrategy
		expectedUpgrade action.DryRunStrategy
		description     string
	}{
		// Normal execution cases
		{
			name:            "no dry run and no template mode",
			template:        false,
			dryRun:          false,
			dryRunOption:    "",
			expectedInstall: action.DryRunNone,
			expectedUpgrade: action.DryRunNone,
			description:     "Normal execution without dry run",
		},
		{
			name:            "client dry run without template mode",
			template:        false,
			dryRun:          true,
			dryRunOption:    "",
			expectedInstall: action.DryRunClient,
			expectedUpgrade: action.DryRunClient,
			description:     "Client-side dry run for validation",
		},
		{
			name:            "server dry run without template mode",
			template:        false,
			dryRun:          true,
			dryRunOption:    "server",
			expectedInstall: action.DryRunServer,
			expectedUpgrade: action.DryRunServer,
			description:     "Server-side dry run to enable lookup functions",
		},
		// Template mode cases (always uses DryRunClient)
		{
			name:            "template mode without dry run",
			template:        true,
			dryRun:          false,
			dryRunOption:    "",
			expectedInstall: action.DryRunClient,
			expectedUpgrade: action.DryRunClient,
			description:     "Template mode always uses DryRunClient to prevent cluster interaction",
		},
		{
			name:            "template mode overrides server dry run",
			template:        true,
			dryRun:          true,
			dryRunOption:    "server",
			expectedInstall: action.DryRunClient,
			expectedUpgrade: action.DryRunClient,
			description:     "Template mode takes precedence over server dry run configuration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Helm{
				template: tt.template,
			}

			// Test Install action
			installAction := &action.Install{}
			h.configureInstallAction(
				installAction,
				&action.Configuration{},
				"test-release",
				"test-namespace",
				time.Duration(0),
				fleet.BundleDeploymentOptions{
					Helm: &fleet.HelmOptions{},
				},
				nil,
				dryRunConfig{
					DryRun:       tt.dryRun,
					DryRunOption: tt.dryRunOption,
				},
			)
			a.Equal(tt.expectedInstall, installAction.DryRunStrategy,
				"Install: %s - expected DryRunStrategy=%v, got %v",
				tt.description, tt.expectedInstall, installAction.DryRunStrategy)

			// Test Upgrade action
			upgradeAction := &action.Upgrade{}
			h.configureUpgradeAction(
				upgradeAction,
				"test-namespace",
				time.Duration(0),
				fleet.BundleDeploymentOptions{
					Helm: &fleet.HelmOptions{},
				},
				nil,
				dryRunConfig{
					DryRun:       tt.dryRun,
					DryRunOption: tt.dryRunOption,
				},
			)
			a.Equal(tt.expectedUpgrade, upgradeAction.DryRunStrategy,
				"Upgrade: %s - expected DryRunStrategy=%v, got %v",
				tt.description, tt.expectedUpgrade, upgradeAction.DryRunStrategy)
		})
	}
}

func TestWaitStrategyConfiguration(t *testing.T) {
	a := assert.New(t)
	h := &Helm{}

	tests := []struct {
		name            string
		timeout         time.Duration
		expectedInstall kube.WaitStrategy
		expectedUpgrade kube.WaitStrategy
	}{
		{
			name:            "no timeout uses HookOnlyStrategy",
			timeout:         0,
			expectedInstall: kube.HookOnlyStrategy,
			expectedUpgrade: kube.HookOnlyStrategy,
		},
		{
			name:            "with timeout uses StatusWatcherStrategy",
			timeout:         5 * time.Minute,
			expectedInstall: kube.StatusWatcherStrategy,
			expectedUpgrade: kube.StatusWatcherStrategy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			installAction := &action.Install{}
			h.configureInstallAction(
				installAction,
				&action.Configuration{},
				"test-release",
				"test-namespace",
				tt.timeout,
				fleet.BundleDeploymentOptions{
					Helm: &fleet.HelmOptions{},
				},
				nil,
				dryRunConfig{DryRun: false},
			)
			a.Equal(tt.expectedInstall, installAction.WaitStrategy, "Install: WaitStrategy should be set based on timeout")

			upgradeAction := &action.Upgrade{}
			h.configureUpgradeAction(
				upgradeAction,
				"test-namespace",
				tt.timeout,
				fleet.BundleDeploymentOptions{
					Helm: &fleet.HelmOptions{},
				},
				nil,
				dryRunConfig{DryRun: false},
			)
			a.Equal(tt.expectedUpgrade, upgradeAction.WaitStrategy, "Upgrade: WaitStrategy should be set based on timeout")
		})
	}
}

func TestServerSideApplyConfiguration(t *testing.T) {
	a := assert.New(t)
	h := &Helm{}

	t.Run("Install with TakeOwnership disables ServerSideApply", func(t *testing.T) {
		installAction := &action.Install{}
		h.configureInstallAction(
			installAction,
			&action.Configuration{},
			"test-release",
			"test-namespace",
			time.Duration(0),
			fleet.BundleDeploymentOptions{
				Helm: &fleet.HelmOptions{
					TakeOwnership: true,
				},
			},
			nil,
			dryRunConfig{DryRun: false},
		)
		a.False(installAction.ServerSideApply, "ServerSideApply should be false when TakeOwnership is true")
	})

	t.Run("Install without TakeOwnership keeps ServerSideApply default", func(t *testing.T) {
		installAction := &action.Install{}
		h.configureInstallAction(
			installAction,
			&action.Configuration{},
			"test-release",
			"test-namespace",
			time.Duration(0),
			fleet.BundleDeploymentOptions{
				Helm: &fleet.HelmOptions{
					TakeOwnership: false,
				},
			},
			nil,
			dryRunConfig{DryRun: false},
		)
		// When TakeOwnership is false, ServerSideApply remains at its zero value (false)
		// but it's not explicitly set, so Helm would use its default behavior
		a.False(installAction.ServerSideApply)
	})

	t.Run("Upgrade uses auto mode for ServerSideApply", func(t *testing.T) {
		upgradeAction := &action.Upgrade{}
		h.configureUpgradeAction(
			upgradeAction,
			"test-namespace",
			time.Duration(0),
			fleet.BundleDeploymentOptions{
				Helm: &fleet.HelmOptions{},
			},
			nil,
			dryRunConfig{DryRun: false},
		)
		a.Equal("auto", upgradeAction.ServerSideApply, "Upgrade should use 'auto' mode for ServerSideApply")
	})
}

func TestCorrectDriftForceOption(t *testing.T) {
	a := assert.New(t)
	h := &Helm{}

	t.Run("CorrectDrift.Force is combined with Helm.Force", func(t *testing.T) {
		tests := []struct {
			name          string
			helmForce     bool
			driftForce    bool
			expectedForce bool
		}{
			{
				name:          "both false",
				helmForce:     false,
				driftForce:    false,
				expectedForce: false,
			},
			{
				name:          "helm true, drift false",
				helmForce:     true,
				driftForce:    false,
				expectedForce: true,
			},
			{
				name:          "helm false, drift true",
				helmForce:     false,
				driftForce:    true,
				expectedForce: true,
			},
			{
				name:          "both true",
				helmForce:     true,
				driftForce:    true,
				expectedForce: true,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				upgradeAction := &action.Upgrade{}
				h.configureUpgradeAction(
					upgradeAction,
					"test-namespace",
					time.Duration(0),
					fleet.BundleDeploymentOptions{
						Helm: &fleet.HelmOptions{
							Force: tt.helmForce,
						},
						CorrectDrift: &fleet.CorrectDrift{
							Force: tt.driftForce,
						},
					},
					nil,
					dryRunConfig{DryRun: false},
				)
				a.Equal(tt.expectedForce, upgradeAction.ForceReplace, "ForceReplace should combine Helm.Force and CorrectDrift.Force")
			})
		}
	})
}

func TestIsInDownstreamResources(t *testing.T) {
	a := assert.New(t)

	opts := fleet.BundleDeploymentOptions{
		DownstreamResources: []fleet.DownstreamResource{
			{Kind: "ConfigMap", Name: "my-config"},
			{Kind: "Secret", Name: "some-secret"},
		},
	}

	// function returns only a boolean indicating membership for a kind
	// enable experimental feature for this test
	os.Setenv(experimental.CopyResourcesDownstreamFlag, "true")
	defer os.Unsetenv(experimental.CopyResourcesDownstreamFlag)

	found := isInDownstreamResources("my-config", "ConfigMap", opts)
	a.True(found, "expected to find my-config in DownstreamResources")

	found2 := isInDownstreamResources("not-present", "ConfigMap", opts)
	a.False(found2, "expected not to find not-present in DownstreamResources")

	found3 := isInDownstreamResources("my-config", "SomeOtherKind", opts)
	a.False(found3, "expected not to find my-config of kind SomeOtherKind in DownstreamResources")

	found4 := isInDownstreamResources("some-secret", "Secret", opts)
	a.True(found4, "expected to find some-secret in DownstreamResources")

	found5 := isInDownstreamResources("not-present", "Secret", opts)
	a.False(found5, "expected not to find not-present in DownstreamResources")
}

func TestValuesFromUsesDefaultNamespaceWhenResourceCopiedDownstream(t *testing.T) {
	a := assert.New(t)
	r := require.New(t)

	scheme := kruntime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	// default namespace where the Helm release lives
	defaultNS := "helm-default"

	// Create a ConfigMap and Secret in the default namespace which should be picked
	// when the valuesFrom reference is part of DownstreamResources.
	cm := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cm-down", Namespace: defaultNS},
		Data:       map[string]string{DefaultKey: "cmVal: cmDefault"},
	}

	sec := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "sec-down", Namespace: defaultNS},
		Data:       map[string][]byte{DefaultKey: []byte("secVal: secDefault")},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&cm, &sec).Build()

	h := &Helm{client: cl, template: false}

	opts := fleet.BundleDeploymentOptions{
		Helm: &fleet.HelmOptions{
			ValuesFrom: []fleet.ValuesFrom{
				{ConfigMapKeyRef: &fleet.ConfigMapKeySelector{LocalObjectReference: fleet.LocalObjectReference{Name: "cm-down"}}},
				{SecretKeyRef: &fleet.SecretKeySelector{LocalObjectReference: fleet.LocalObjectReference{Name: "sec-down"}, Namespace: "ignored-ns"}},
			},
		},
		// copied resources: meaning these exist in the defaultNamespace of the release
		DownstreamResources: []fleet.DownstreamResource{{Kind: "ConfigMap", Name: "cm-down"}, {Kind: "Secret", Name: "sec-down"}},
	}

	// enable experimental copy behavior for this test
	os.Setenv(experimental.CopyResourcesDownstreamFlag, "true")
	defer os.Unsetenv(experimental.CopyResourcesDownstreamFlag)

	vals, err := h.getValues(context.TODO(), opts, defaultNS)
	r.NoError(err)

	// configmap and secret data should have been read from defaultNS
	a.Equal("cmDefault", vals["cmVal"])
	a.Equal("secDefault", vals["secVal"])
}

func TestValuesFromUsesProvidedNamespaceWhenNotCopiedDownstream(t *testing.T) {
	a := assert.New(t)
	r := require.New(t)

	scheme := kruntime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	// namespaces
	defaultNS := "helm-default"
	providedNS := "explicit-ns"

	// ConfigMap present only in providedNS
	cmProvided := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cm-provided", Namespace: providedNS},
		Data:       map[string]string{DefaultKey: "cmVal: cmProvided"},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&cmProvided).Build()
	h := &Helm{client: cl, template: false}

	opts := fleet.BundleDeploymentOptions{
		Helm: &fleet.HelmOptions{
			ValuesFrom: []fleet.ValuesFrom{{ConfigMapKeyRef: &fleet.ConfigMapKeySelector{LocalObjectReference: fleet.LocalObjectReference{Name: "cm-provided"}, Namespace: providedNS}}},
		},
		// DownstreamResources does NOT list cm-provided — so the provided namespace should be used
		DownstreamResources: []fleet.DownstreamResource{{Kind: "ConfigMap", Name: "some-other"}},
	}

	vals, err := h.getValues(context.TODO(), opts, defaultNS)
	r.NoError(err)
	a.Equal("cmProvided", vals["cmVal"])
}

func TestValuesFromErrorWhenCopiedDownstreamButExperimentalDisabled(t *testing.T) {
	a := assert.New(t)
	r := require.New(t)

	scheme := kruntime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	h := &Helm{client: cl, template: false}

	opts := fleet.BundleDeploymentOptions{
		Helm: &fleet.HelmOptions{
			ValuesFrom: []fleet.ValuesFrom{
				{ConfigMapKeyRef: &fleet.ConfigMapKeySelector{LocalObjectReference: fleet.LocalObjectReference{Name: "cm-down"}, Namespace: "provided-ns"}},
				{SecretKeyRef: &fleet.SecretKeySelector{LocalObjectReference: fleet.LocalObjectReference{Name: "sec-down"}, Namespace: "provided-ns"}},
			},
		},
		DownstreamResources: []fleet.DownstreamResource{{Kind: "ConfigMap", Name: "cm-down"}, {Kind: "Secret", Name: "sec-down"}},
	}

	// ensure experimental feature is disabled
	os.Unsetenv(experimental.CopyResourcesDownstreamFlag)

	_, err := h.getValues(context.TODO(), opts, "default-ns")
	r.Error(err)
	// get will fail trying to read from provided-ns and should report not found
	a.True(apierrors.IsNotFound(err), "expected a NotFound error when valuesFrom references resources and experimental feature is disabled")
}
