gitjobs
========

Job controller to launch kubernetes jobs based on git event

## Building

`make`

## Running

```bash
kubectl apply -f https://raw.githubusercontent.com/StrongMonkey/gitjobs/master/manifest/gitjobs.yaml
```

## Usage

Gitjobs allows you to launch [kubernetes jobs](https://kubernetes.io/docs/concepts/workloads/controllers/job/) based on git event. By default it uses polling to receive git event, but also can be configured to use webhook.

### Example

To run `kubectl apply` on a github repo:

1. First, create a serviceAccount and rbac roles so that you have sufficient privileges to create resources.

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: kubectl-apply
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: kubectl-apply
rules:
  - apiGroups:
    - "apps"
    resources:
    - 'deployments'
    verbs:
    - '*'
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: kubectl-apply
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: kubectl-apply
subjects:
  - kind: ServiceAccount
    name: kubectl-apply
```

2. Create a gitjob CRD that apply manifest when git repo changes.(Using polling)

```yaml
apiVersion: gitops.cattle.io/v1
kind: GitJob
metadata:
  name: example
  namespace: default
spec:
  git:
    branch: master
    repo: https://github.com/StrongMonkey/gitjobs-example
    provider: polling
  jobSpec:
    template:
      spec:
        serviceAccountName: kubectl-apply
        restartPolicy: "Never"
        containers:
        - image: "bitnami/kubectl:latest"
          name: kubectl-apply
          command:
          - kubectl
          args:
          - apply
          - -f
          - deployment.yaml
          workingDir: /workspace/source
```

Note: Git repository will be cloned under `/workspace/source` by default.

3. A kubernetes job will be created with specified job template. 

```bash
NAME                    COMPLETIONS   DURATION   AGE
example-3af7c           1/1           5s         24h
```

### Private repo

For private repo that needs credential:

1. Create a kubernetes secret that contains ssh-private-key.

```bash
kubectl create secret generic ssh-key-secret --from-file=ssh-privatekey=/path/to/private-key
```

2. Apply a gitjob CRD with secret specified.

```yaml
apiVersion: gitops.cattle.io/v1
kind: GitJob
metadata:
  name: example-private
spec:
  git:
    branch: master
    repo: git@github.com:StrongMonkey/priv-repo.git
    provider: polling
    gitSecretName: ssh-key-secret
    gitSecretType: ssh
    gitHostName: github.com
  jobSpec:
    template:
      spec:
        serviceAccountName: kubectl-apply
        restartPolicy: "Never"
        containers:
          - image: "bitnami/kubectl:latest"
            name: kubectl-apply
            command:
              - kubectl
            args:
              - apply
              - -f
              - deployment.yaml
            workingDir: /workspace/source
```



## License
Copyright (c) 2020 [Rancher Labs, Inc.](http://rancher.com)

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

[http://www.apache.org/licenses/LICENSE-2.0](http://www.apache.org/licenses/LICENSE-2.0)

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
