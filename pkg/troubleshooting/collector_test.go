package troubleshooting

import (
	"reflect"
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Payloads and their digests, shared by both tests.
//
// The digests are literals on purpose. Computing them with helmvalues would
// only assert that the code equals itself, and would still pass if the
// arguments to HashOptions were swapped. They were derived independently with
// sha256sum, e.g.
//
//	printf '{"foo":"bar"}{"foo":"baz"}' | sha256sum
//	printf '{"values.yaml":"eyJmb28iOiJiYXIifQ=="}' | sha256sum
//
// The second form is what json.Marshal produces for a map[string][]byte:
// keys sorted, values base64-encoded.
var (
	valuesBar = []byte(`{"foo":"bar"}`)
	valuesBaz = []byte(`{"foo":"baz"}`)
)

const (
	// hash of a bundle values secret holding {"values.yaml": valuesBar}
	bundleValuesBarHash = "33f746f3114036db34d18b1f8d638b614910073506377f0011b1311d1f5d5062"
	// same, holding {"values.yaml": valuesBaz}
	bundleValuesBazHash = "f4aa0776a38fc5c811bd998514ef368d1079fcad8b80f996de1367cd7b380c9e"
	// hash of an options secret holding values=valuesBar, stagedValues=valuesBaz
	optionsBarBazHash = "2fce18c19ef3b1f257cce9e0eac26a579c7e78ea3daf272724c6e6aee5c70bf9"
	// same, holding values=valuesBaz, stagedValues=valuesBaz
	optionsBazBazHash = "7da4e0de0f0ab1f8d581f3839ce7b86cbcfb9cdbf7eb4363c37bc1743639d754"
)

func Test_secretValuesHash(t *testing.T) {
	testCases := []struct {
		name   string
		secret corev1.Secret
		want   string
	}{
		{
			name: "bundle values secret",
			secret: corev1.Secret{
				Type: fleet.SecretTypeBundleValues,
				Data: map[string][]byte{
					"values.yaml": valuesBar,
				},
			},
			want: bundleValuesBarHash,
		},
		{
			// Keys are marshaled in sorted order, so the hash does not depend
			// on Go's map iteration order.
			name: "bundle values secret with several keys",
			secret: corev1.Secret{
				Type: fleet.SecretTypeBundleValues,
				Data: map[string][]byte{
					"zebra.yaml":  []byte(`{"baz":1}`),
					"values.yaml": valuesBar,
				},
			},
			want: "65123900699a42f46928533b1e5a406306e1e603042fb66b9241c75169675cba",
		},
		{
			// values and stagedValues differ deliberately: hashing them in the
			// wrong order yields a different digest, so this case fails if the
			// arguments to HashOptions are ever swapped.
			name: "bundle deployment options secret",
			secret: corev1.Secret{
				Type: fleet.SecretTypeBundleDeploymentOptions,
				Data: map[string][]byte{
					"values":       valuesBar,
					"stagedValues": valuesBaz,
				},
			},
			want: optionsBarBazHash,
		},
		{
			// A missing staged key contributes no bytes at all, so the digest
			// matches that of the values key on its own.
			name: "bundle deployment options secret without staged values",
			secret: corev1.Secret{
				Type: fleet.SecretTypeBundleDeploymentOptions,
				Data: map[string][]byte{
					"values": valuesBar,
				},
			},
			want: "7a38bf81f383f69433ad6e900d35b3e2385593f76a7b7ab5d4355b8ba41ee24b",
		},
		{
			// Secrets Fleet does not own are ignored rather than hashed, so
			// they never show up as a values hash mismatch.
			name: "unrelated secret type",
			secret: corev1.Secret{
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{
					"values": valuesBar,
				},
			},
			want: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := secretValuesHash(tc.secret)

			if got != tc.want {
				t.Fatalf("expected hash %q, got %q", tc.want, got)
			}
		})
	}
}

func Test_detectSecretsWithValuesHashMismatch(t *testing.T) {
	deleted := metav1.Now()

	testCases := []struct {
		name              string
		bundles           []fleet.Bundle
		bundleDeployments []fleet.BundleDeployment
		secrets           []corev1.Secret
		want              []ValuesHashMismatch
	}{
		{
			name:    "bundle secret matching its bundle",
			bundles: []fleet.Bundle{bundle(bundleUID)},
			secrets: []corev1.Secret{valuesSecret(bundleUID, valuesBar)},
			want:    nil,
		},
		{
			name:    "bundle secret disagreeing with its bundle",
			bundles: []fleet.Bundle{bundle(bundleUID)},
			secrets: []corev1.Secret{valuesSecret(bundleUID, valuesBaz)},
			want: []ValuesHashMismatch{{
				Namespace:  bundleNamespace,
				Name:       bundleName,
				OwnerKind:  "Bundle",
				SpecHash:   bundleValuesBarHash,
				SecretHash: bundleValuesBazHash,
			}},
		},
		{
			name:              "options secret matching its bundledeployment",
			bundleDeployments: []fleet.BundleDeployment{bundleDeployment(optionsBarBazHash, false)},
			secrets:           []corev1.Secret{optionsSecret(valuesBar)},
			want:              nil,
		},
		{
			name:              "options secret disagreeing with its bundledeployment",
			bundleDeployments: []fleet.BundleDeployment{bundleDeployment(optionsBarBazHash, false)},
			secrets:           []corev1.Secret{optionsSecret(valuesBaz)},
			want: []ValuesHashMismatch{{
				Namespace:  targetNamespace,
				Name:       bundleDeploymentName,
				OwnerKind:  "BundleDeployment",
				SpecHash:   optionsBarBazHash,
				SecretHash: optionsBazBazHash,
			}},
		},
		{
			// repairHashMismatch clears ValuesHash and sets WaitingForValues in
			// the same patch, so the stale secret disagrees with an empty hash
			// by design. Fleet knows and is repairing it, and reporting that
			// would be a false positive.
			name:              "bundledeployment waiting for values is skipped",
			bundleDeployments: []fleet.BundleDeployment{bundleDeployment("", true)},
			secrets:           []corev1.Secret{optionsSecret(valuesBaz)},
			want:              nil,
		},
		{
			// Same empty hash, but nothing claims to be repairing it. That is a
			// genuinely inconsistent BundleDeployment and stays reported.
			name:              "bundledeployment with cleared hash and no repair in flight",
			bundleDeployments: []fleet.BundleDeployment{bundleDeployment("", false)},
			secrets:           []corev1.Secret{optionsSecret(valuesBaz)},
			want: []ValuesHashMismatch{{
				Namespace:  targetNamespace,
				Name:       bundleDeploymentName,
				OwnerKind:  "BundleDeployment",
				SpecHash:   "",
				SecretHash: optionsBazBazHash,
			}},
		},
		{
			// The owner was deleted and recreated, so the secret points at a
			// dead UID. getOrphanedSecrets already reports these.
			name:    "secret whose owner uid no longer matches",
			bundles: []fleet.Bundle{bundle("uid-new")},
			secrets: []corev1.Secret{valuesSecret("uid-old", valuesBaz)},
			want:    nil,
		},
		{
			name:    "secret whose owner is gone",
			bundles: nil,
			secrets: []corev1.Secret{valuesSecret(bundleUID, valuesBaz)},
			want:    nil,
		},
		{
			name:    "secret without owner references",
			bundles: []fleet.Bundle{bundle(bundleUID)},
			secrets: []corev1.Secret{func() corev1.Secret {
				s := valuesSecret(bundleUID, valuesBaz)
				s.OwnerReferences = nil
				return s
			}()},
			want: nil,
		},
		{
			// A secret on its way out will not be re-hashed by anyone.
			name:    "secret being deleted",
			bundles: []fleet.Bundle{bundle(bundleUID)},
			secrets: []corev1.Secret{func() corev1.Secret {
				s := valuesSecret(bundleUID, valuesBaz)
				s.DeletionTimestamp = &deleted
				return s
			}()},
			want: nil,
		},
		{
			name:              "several secrets, only the inconsistent one is reported",
			bundles:           []fleet.Bundle{bundle(bundleUID)},
			bundleDeployments: []fleet.BundleDeployment{bundleDeployment(optionsBarBazHash, false)},
			secrets: []corev1.Secret{
				valuesSecret(bundleUID, valuesBar),
				optionsSecret(valuesBaz),
			},
			want: []ValuesHashMismatch{{
				Namespace:  targetNamespace,
				Name:       bundleDeploymentName,
				OwnerKind:  "BundleDeployment",
				SpecHash:   optionsBarBazHash,
				SecretHash: optionsBazBazHash,
			}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			col := &Collector{}

			got := col.detectSecretsWithValuesHashMismatch(tc.secrets, tc.bundles, tc.bundleDeployments)

			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("expected mismatches %+v, got %+v", tc.want, got)
			}
		})
	}
}

// Fleet names a values secret after the resource that owns it, so the fixtures
// below share one name per owner kind. Only the values that a test case varies
// are parameters; the rest are fixed here to keep the table readable.
const (
	bundleNamespace = "fleet-local"
	targetNamespace = "cluster-fleet-local-local-1a3d67d0a899"

	bundleName           = "b"
	bundleDeploymentName = "bd"

	bundleUID           = "uid-b"
	bundleDeploymentUID = "uid-bd"
)

// bundle returns a Bundle expecting bundleValuesBarHash, the hash of a values
// secret holding valuesBar.
func bundle(uid string) fleet.Bundle {
	return fleet.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: bundleNamespace,
			Name:      bundleName,
			UID:       types.UID(uid),
		},
		Spec: fleet.BundleSpec{ValuesHash: bundleValuesBarHash},
	}
}

func bundleDeployment(valuesHash string, waiting bool) fleet.BundleDeployment {
	return fleet.BundleDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      bundleDeploymentName,
			UID:       types.UID(bundleDeploymentUID),
		},
		Spec: fleet.BundleDeploymentSpec{
			ValuesHash:       valuesHash,
			WaitingForValues: waiting,
		},
	}
}

func valuesSecret(ownerUID string, values []byte) corev1.Secret {
	return corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       bundleNamespace,
			Name:            bundleName,
			OwnerReferences: []metav1.OwnerReference{ownerRef("Bundle", bundleName, ownerUID)},
		},
		Type: fleet.SecretTypeBundleValues,
		Data: map[string][]byte{"values.yaml": values},
	}
}

// optionsSecret pairs the given values with a fixed stagedValues, so a secret
// holding valuesBar hashes to optionsBarBazHash and one holding valuesBaz
// hashes to optionsBazBazHash.
func optionsSecret(values []byte) corev1.Secret {
	return corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       targetNamespace,
			Name:            bundleDeploymentName,
			OwnerReferences: []metav1.OwnerReference{ownerRef("BundleDeployment", bundleDeploymentName, bundleDeploymentUID)},
		},
		Type: fleet.SecretTypeBundleDeploymentOptions,
		Data: map[string][]byte{
			"values":       values,
			"stagedValues": valuesBaz,
		},
	}
}

func ownerRef(kind, name, uid string) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: fleet.SchemeGroupVersion.String(),
		Kind:       kind,
		Name:       name,
		UID:        types.UID(uid),
	}
}
