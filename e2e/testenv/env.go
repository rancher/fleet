// Package testenv contains common helpers for tests
package testenv

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/rancher/fleet/e2e/testenv/kubectl"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

const (
	Timeout       = 5 * time.Minute
	ShortTimeout  = 5 * time.Second
	MediumTimeout = 120 * time.Second

	// PodReadyTimeout is the timeout for waiting for pods to become ready in test
	// infrastructure setup
	// Set to 180s to accommodate startup probe (160s max: 10s initial delay + (30 failures × 5s period))
	PodReadyTimeout = 180 * time.Second

	// LongTimeout is an extended timeout for slower CI environments
	LongTimeout = 10 * time.Minute
	// VeryLongTimeout is a very long timeout for slower CI environments
	VeryLongTimeout = 15 * time.Minute

	// PollingInterval is the polling interval for Eventually assertions
	PollingInterval = 2 * time.Second
	// LongPollingInterval is a longer polling interval for Eventually assertions
	LongPollingInterval = 5 * time.Second
)

type Env struct {
	Kubectl kubectl.Command
	// Upstream context for cluster containing the fleet controller and local agent
	Upstream string
	// Downstream context for fleet-agent cluster
	Downstream string
	// Managed downstream cluster
	ManagedDownstream string
	// Namespace which contains the cluster resource for most E2E tests
	// (cluster registration namespace)
	Namespace string
	// Namespace which contains downstream clusters, besides the local
	// cluster, for some multi-cluster E2E tests
	ClusterRegistrationNamespace string
}

func New() *Env {
	env := &Env{
		Kubectl:                      kubectl.New("", "default"),
		Upstream:                     "k3d-upstream",
		Downstream:                   "k3d-downstream",
		ManagedDownstream:            "k3d-managed-downstream",
		Namespace:                    "fleet-local",
		ClusterRegistrationNamespace: "fleet-default",
	}
	env.getShellEnv()
	return env
}

func (e *Env) getShellEnv() {
	if val := os.Getenv("FLEET_E2E_CLUSTER"); val != "" {
		e.Upstream = val
	}
	if val := os.Getenv("FLEET_E2E_CLUSTER_DOWNSTREAM"); val != "" {
		e.Downstream = val
	}
	if val := os.Getenv("FLEET_E2E_NS"); val != "" {
		e.Namespace = val
	}
	if val := os.Getenv("FLEET_E2E_NS_DOWNSTREAM"); val != "" {
		e.ClusterRegistrationNamespace = val
	}
}

// GetCluster retrieves the cluster resource from the API server and unmarshals it.
func (e *Env) GetCluster(name, namespace string) (*fleet.Cluster, error) {
	out, err := e.Kubectl.Namespace(namespace).Get("cluster", name, "-o", "json")
	if err != nil {
		return nil, err
	}
	cluster := &fleet.Cluster{}
	return cluster, e.Unmarshal(out, cluster)
}

// Unmarshal unmarshals the given JSON string into the provided object.
func (e *Env) Unmarshal(out string, obj interface{}) error {
	return json.Unmarshal([]byte(out), obj)
}

// NewNamespaceName returns a name for a namespace that is unique to the test
// run. e.g. as a targetNamespace for workloads
func NewNamespaceName(name string, s rand.Source) string {
	p := make([]byte, 12)
	r := rand.New(s)
	_, err := r.Read(p)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("test-%.20s-%.12s", name, hex.EncodeToString(p))
}

// AddRandomSuffix adds a random suffix to a given name.
func AddRandomSuffix(name string, s rand.Source) string {
	p := make([]byte, 6)
	r := rand.New(s)
	_, err := r.Read(p)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%s-%s", name, hex.EncodeToString(p))
}
