gitjob
========

Job controller to launch kubernetes jobs based on git event

## Building

`make`

## Running

1. Download helm chart releases from [releases pages](https://github.com/rancher/gitjob/releases)

2. Install the helm chart.

```bash
kubectl create namespace gitjob
helm install gitjob --namespace gitjob ./path/to/your/helm/tarball
```

## Usage

gitjob allows you to launch [kubernetes jobs](https://kubernetes.io/docs/concepts/workloads/controllers/job/) based on git event. By default it uses polling to receive git event, but also can be configured to use webhook.

### Quick start

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
apiVersion: gitjob.cattle.io/v1
kind: GitJob
metadata:
  name: example
  namespace: default
spec:
  syncInterval: 15  // in seconds, default to 15 
  git:
    branch: master
    repo: https://github.com/StrongMonkey/gitjob-example
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

Two environmental variables: `COMMIT`, `EVENT_TYPE` will be added into your job spec.

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
apiVersion: gitjob.cattle.io/v1
kind: GitJob
metadata:
  name: example-private
spec:
  git:
    branch: master
    repo: git@github.com:StrongMonkey/priv-repo.git
    provider: polling
    gitSecretName: ssh-key-secret
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

### Webhook

gitjob can be configured to use webhook to receive git event. This currently supports Github. More providers will be added later.

1. Create a gitjob that is configured with webhook.

```yaml
apiVersion: gitjob.cattle.io/v1
kind: GitJob
metadata:
  name: example-webhook
  namespace: default
spec:
  git:
    branch: master
    repo: https://github.com/StrongMonkey/gitjob-example
    provider: github
    github:
      token: randomtoken
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

Note: you can configure a secret token so that webhook server will validate the request and filter requests that are only coming from Github.

2. Create an ingress that allows traffic.

```yaml
apiVersion: networking.k8s.io/v1beta1
kind: Ingress
metadata:
  name: webhook-ingress
  namespace: gitjob
spec:
  rules:
  - host: your.domain.com
    http:
      paths:
        - path: /hooks
          pathType: Prefix
          backend:
            serviceName: gitjob
            servicePort: 80
```

Note: To configure a HTTPS receiver, make sure you have proper TLS configuration on your ingress

3. Create a Github webhook that sends payload to `http://your.domain.com/hooks?gitjobId=default:example-webhook`.

![webhook](/webhook.png)

You can choose which event to send when creating the webhook. Gitjob currently supports push and pull-request event.

#### Auto-Configuring github webhook

GitJob will create webhook for you if you have proper setting created

1. Create a configmap in kube-system namespace

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: github-setting
  namespace: kube-system
data:
  WebhookURL: https://webhook.example.com  #This will be your webhook callback URL
  SecretName: githubtoken
```

2. Create a secret that contains your github access token

```bash
kubectl create secret generic -n kube-system githubtoken --from-literal=token=$ACCESS_TOKEN
```

3. Create a gitjob CR and set provider to github

```yaml
apiVersion: gitjob.cattle.io/v1
kind: GitJob
metadata:
  name: example-webhook
  namespace: default
spec:
  git:
    branch: master
    repo: https://github.com/StrongMonkey/gitjob-example
    provider: github
  jobSpec:
    ...
```

GitJob controller will automatically create webhook with callback URL `https://webhook.example.com?gitjobId=default:example-webhook` based on the global setting. At this time it doesn't delete webhook if CR is deleted from cluster, so make sure to clean up webhook if not used.

4. Setup ingress and TLS to allow traffic to go into GitJob controller so that it can start receiving events.

```yaml
apiVersion: networking.k8s.io/v1beta1
kind: Ingress
metadata:
  name: webhook-ingress
  namespace: gitjob
spec:
  rules:
  - host: webhook.example.com
    http:
      paths:
        - pathType: Prefix
          backend:
            serviceName: gitjob
            servicePort: 80
  tls:
    - hosts:
        - webhook.example.com
      secretName: testsecret-tls
```

### API reference

API types are defined in [here](./pkg/apis/gitjob.cattle.io/v1/types.go)

## Contribution 

Part of this project is built upon [Tekton](https://github.com/tektoncd).

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
