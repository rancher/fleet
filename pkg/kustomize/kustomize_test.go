package kustomize

import (
	"strings"
	"testing"

	"github.com/rancher/wrangler/pkg/yaml"
	"github.com/stretchr/testify/assert"

	kusttest_test "sigs.k8s.io/kustomize/api/testutils/kusttest"
	"sigs.k8s.io/kustomize/api/types"
)

func assertActualTrimSpaceEqualsExpected(t *testing.T, actual string, expected string) {
	assert.Equal(t, strings.TrimSpace(expected), strings.TrimSpace(actual))
}

func writeSmallBase(th kusttest_test.Harness) {
	th.WriteK("/app/base", `
namePrefix: a-
commonLabels:
  app: myApp
resources:
- deployment.yaml
- service.yaml
`)
	th.WriteF("/app/base/service.yaml", `
apiVersion: v1
kind: Service
metadata:
  name: myService
spec:
  selector:
    backend: bungie
  ports:
    - port: 7002
`)
	th.WriteF("/app/base/deployment.yaml", `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: myDeployment
spec:
  template:
    metadata:
      labels:
        backend: awesome
    spec:
      containers:
      - name: whatever
        image: whatever
`)
}

func TestSmallOverlay(t *testing.T) {
	th := kusttest_test.MakeHarness(t)
	writeSmallBase(th)
	th.WriteK("/app/overlay", `
namePrefix: b-
commonLabels:
  env: prod
  quotedFruit: "peach"
  quotedBoolean: "true"
resources:
- ../base
patchesStrategicMerge:
- deployment/deployment.yaml
images:
- name: whatever
  newTag: 1.8.0
`)

	th.WriteF("/app/overlay/configmap/app.env", `
DB_USERNAME=admin
DB_PASSWORD=somepw
`)
	th.WriteF("/app/overlay/configmap/app-init.ini", `
FOO=bar
BAR=baz
`)
	th.WriteF("/app/overlay/deployment/deployment.yaml", `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: myDeployment
spec:
  replicas: 1000
`)
	result, err := kustomize(th.GetFSys(), "/app/overlay", "")
	if err != nil {
		t.Fatalf("kustomize failed: %v", err)
	}
	data, err := yaml.ToBytes(result)
	if err != nil {
		t.Fatalf("yaml.ToBytes failed: %v", err)
	}
	assertActualTrimSpaceEqualsExpected(t, string(data), `
apiVersion: v1
kind: Service
metadata:
  labels:
    app: myApp
    env: prod
    quotedBoolean: "true"
    quotedFruit: peach
  name: b-a-myService
spec:
  ports:
  - port: 7002
  selector:
    app: myApp
    backend: bungie
    env: prod
    quotedBoolean: "true"
    quotedFruit: peach

---
apiVersion: apps/v1
kind: Deployment
metadata:
  labels:
    app: myApp
    env: prod
    quotedBoolean: "true"
    quotedFruit: peach
  name: b-a-myDeployment
spec:
  replicas: 1000
  selector:
    matchLabels:
      app: myApp
      env: prod
      quotedBoolean: "true"
      quotedFruit: peach
  template:
    metadata:
      labels:
        app: myApp
        backend: awesome
        env: prod
        quotedBoolean: "true"
        quotedFruit: peach
    spec:
      containers:
      - image: whatever:1.8.0
        name: whatever
`)
}

func TestSharedPatchDisAllowed(t *testing.T) {
	th := kusttest_test.MakeHarness(t)
	writeSmallBase(th)
	th.WriteK("/app/overlay", `
commonLabels:
  env: prod
resources:
- ../base
patchesStrategicMerge:
- ../shared/deployment-patch.yaml
`)
	th.WriteF("/app/shared/deployment-patch.yaml", `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: myDeployment
spec:
  replicas: 1000
`)
	_, err := kustomize(th.GetFSys(), "/app/overlay", "")
	if !strings.Contains(
		err.Error(),
		"security; file '/app/shared/deployment-patch.yaml' is not in or below '/app/overlay'") {
		t.Fatalf("unexpected error: %s", err)
	}
}

func TestSharedPatchAllowed(t *testing.T) {
	th := kusttest_test.MakeHarness(t)
	writeSmallBase(th)
	th.WriteK("/app/overlay", `
commonLabels:
  env: prod
resources:
- ../base
patchesStrategicMerge:
- ../shared/deployment-patch.yaml
`)
	th.WriteF("/app/shared/deployment-patch.yaml", `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: myDeployment
spec:
  replicas: 1000
`)
	result, err := kustomize(th.GetFSys(), "/app/overlay", "--load-restrictor "+types.LoadRestrictionsNone.String())
	if err != nil {
		t.Fatalf("kustomize failed: %v", err)
	}
	data, err := yaml.ToBytes(result)
	if err != nil {
		t.Fatalf("yaml.ToBytes failed: %v", err)
	}
	assertActualTrimSpaceEqualsExpected(t, string(data), `
apiVersion: v1
kind: Service
metadata:
  labels:
    app: myApp
    env: prod
  name: a-myService
spec:
  ports:
  - port: 7002
  selector:
    app: myApp
    backend: bungie
    env: prod

---
apiVersion: apps/v1
kind: Deployment
metadata:
  labels:
    app: myApp
    env: prod
  name: a-myDeployment
spec:
  replicas: 1000
  selector:
    matchLabels:
      app: myApp
      env: prod
  template:
    metadata:
      labels:
        app: myApp
        backend: awesome
        env: prod
    spec:
      containers:
      - image: whatever
        name: whatever
`)
}
