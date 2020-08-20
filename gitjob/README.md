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

<p>Packages:</p>
<ul>
<li>
<a href="#gitjob.cattle.io%2fv1">gitjob.cattle.io/v1</a>
</li>
</ul>
<h2 id="gitjob.cattle.io/v1">gitjob.cattle.io/v1</h2>
<p>
</p>
Resource Types:
<ul><li>
<a href="#gitjob.cattle.io/v1.GitJob">GitJob</a>
</li></ul>
<h3 id="GitJob">GitJob
</h3>
<p>
</p>
<table>
<thead>
<tr>
<th>Field</th>
<th>Description</th>
</tr>
</thead>
<tbody>
<tr>
<td>
<code>apiVersion</code></br>
string</td>
<td>
<code>
gitjob.cattle.io/v1
</code>
</td>
</tr>
<tr>
<td>
<code>kind</code></br>
string
</td>
<td><code>GitJob</code></td>
</tr>
<tr>
<td>
<code>metadata</code></br>
<em>
<a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.13/#objectmeta-v1-meta">
Kubernetes meta/v1.ObjectMeta
</a>
</em>
</td>
<td>
Refer to the Kubernetes API documentation for the fields of the
<code>metadata</code> field.
</td>
</tr>
<tr>
<td>
<code>spec</code></br>
<em>
<a href="#gitjob.cattle.io/v1.GitJobSpec">
GitJobSpec
</a>
</em>
</td>
<td>
<br/>
<br/>
<table>
<tr>
<td>
<code>git</code></br>
<em>
<a href="#gitjob.cattle.io/v1.GitInfo">
GitInfo
</a>
</em>
</td>
<td>
<p>Git metadata information</p>
</td>
</tr>
<tr>
<td>
<code>jobSpec</code></br>
<em>
<a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.13/#jobspec-v1-batch">
Kubernetes batch/v1.JobSpec
</a>
</em>
</td>
<td>
<p>Job template applied to git commit</p>
</td>
</tr>
<tr>
<td>
<code>syncInterval</code></br>
<em>
int
</em>
</td>
<td>
<p>define interval(in seconds) for controller to sync repo and fetch commits</p>
</td>
</tr>
</table>
</td>
</tr>
<tr>
<td>
<code>status</code></br>
<em>
<a href="#gitjob.cattle.io/v1.GitJobStatus">
GitJobStatus
</a>
</em>
</td>
<td>
</td>
</tr>
</tbody>
</table>
<h3 id="Credential">Credential
</h3>
<p>
(<em>Appears on:</em>
<a href="#github.com%2francher%2fgitjob%2fpkg%2fapis%2fgitjob.cattle.io%2fv1.GitInfo">GitInfo</a>)
</p>
<p>
</p>
<table>
<thead>
<tr>
<th>Field</th>
<th>Description</th>
</tr>
</thead>
<tbody>
<tr>
<td>
<code>caBundle</code></br>
<em>
[]byte
</em>
</td>
<td>
<p>CABundle is a PEM encoded CA bundle which will be used to validate the repo&rsquo;s certificate.</p>
</td>
</tr>
<tr create mode 100644 docs/pkg.tpl
>
<td>
<code>insecureSkipTLSVerify</code></br>
<em>
bool
</em>
</td>
<td>
<p>InsecureSkipTLSverify will use insecure HTTPS to download the repo&rsquo;s index.</p>
</td>
</tr>
<tr>
<td>
<code>gitHostName</code></br>
<em>
string
</em>
</td>
<td>
<p>Hostname of git server</p>
</td>
</tr>
<tr>
<td>
<code>gitSecretName</code></br>
<em>
string
</em>
</td>
<td>
<p>Secret Name of git credential</p>
</td>
</tr>
</tbody>
</table>
<h3 id="GitEvent">GitEvent
</h3>
<p>
(<em>Appears on:</em>
<a href="#github.com%2francher%2fgitjob%2fpkg%2fapis%2fgitjob.cattle.io%2fv1.GitJobStatus">GitJobStatus</a>)
</p>
<p>
</p>
<table>
<thead>
<tr>
<th>Field</th>
<th>Description</th>
</tr>
</thead>
<tbody>
<tr>
<td>
<code>commit</code></br>
<em>
string
</em>
</td>
<td>
<p>The latest commit SHA received from git repo</p>
</td>
</tr>
<tr>
<td>
<code>lastExecutedCommit</code></br>
<em>
string
</em>
</td>
<td>
<p>Last executed commit SHA by gitjob controller</p>
</td>
</tr>
<tr>
<td>
<code>GithubMeta</code></br>
<em>
<a href="#gitjob.cattle.io/v1.GithubMeta">
GithubMeta
</a>
</em>
</td>
<td>
</td>
</tr>
</tbody>
</table>
<h3 id="GitInfo">GitInfo
</h3>
<p>
(<em>Appears on:</em>
<a href="#github.com%2francher%2fgitjob%2fpkg%2fapis%2fgitjob.cattle.io%2fv1.GitJobSpec">GitJobSpec</a>)
</p>
<p>
</p>
<table>
<thead>
<tr>
<th>Field</th>
<th>Description</th>
</tr>
</thead>
<tbody>
<tr>
<td>
<code>Credential</code></br>
<em>
<a href="#gitjob.cattle.io/v1.Credential">
Credential
</a>
</em>
</td>
<td>
<p>Git credential metadata</p>
</td>
</tr>
<tr>
<td>
<code>provider</code></br>
<em>
string
</em>
</td>
<td>
<p>Git provider model to fetch commit. Can be polling(regular git fetch)/webhook(github webhook)</p>
</td>
</tr>
<tr>
<td>
<code>repo</code></br>
<em>
string
</em>
</td>
<td>
<p>Git repo URL</p>
</td>
</tr>
<tr>
<td>
<code>revision</code></br>
<em>
string
</em>
</td>
<td>
<p>Git commit SHA. If specified, controller will use this SHA instead of auto-fetching commit</p>
</td>
</tr>
<tr>
<td>
<code>branch</code></br>
<em>
string
</em>
</td>
<td>
<p>Git branch to watch. Default to master</p>
</td>
</tr>
<tr>
<td>
<code>Github</code></br>
<em>
<a href="#gitjob.cattle.io/v1.Github">
Github
</a>
</em>
</td>
<td>
</td>
</tr>
</tbody>
</table>
<h3 id="GitJobSpec">GitJobSpec
</h3>
<p>
(<em>Appears on:</em>
<a href="#github.com%2francher%2fgitjob%2fpkg%2fapis%2fgitjob.cattle.io%2fv1.GitJob">GitJob</a>)
</p>
<p>
</p>
<table>
<thead>
<tr>
<th>Field</th>
<th>Description</th>
</tr>
</thead>
<tbody>
<tr>
<td>
<code>git</code></br>
<em>
<a href="#gitjob.cattle.io/v1.GitInfo">
GitInfo
</a>
</em>
</td>
<td>
<p>Git metadata information</p>
</td>
</tr>
<tr>
<td>
<code>jobSpec</code></br>
<em>
<a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.13/#jobspec-v1-batch">
Kubernetes batch/v1.JobSpec
</a>
</em>
</td>
<td>
<p>Job template applied to git commit</p>
</td>
</tr>
<tr>
<td>
<code>syncInterval</code></br>
<em>
int
</em>
</td>
<td>
<p>define interval(in seconds) for controller to sync repo and fetch commits</p>
</td>
</tr>
</tbody>
</table>
<h3 id="GitJobStatus">GitJobStatus
</h3>
<p>
(<em>Appears on:</em>
<a href="#github.com%2francher%2fgitjob%2fpkg%2fapis%2fgitjob.cattle.io%2fv1.GitJob">GitJob</a>)
</p>
<p>
</p>
<table>
<thead<p>Packages:</p>
<ul>
<li>
<a href="#gitjob.cattle.io%2fv1">gitjob.cattle.io/v1</a>
</li>
</ul>
<h2 id="gitjob.cattle.io/v1">gitjob.cattle.io/v1</h2>
<p>
</p>
Resource Types:
<ul><li>
<a href="#gitjob.cattle.io/v1.GitJob">GitJob</a>
</li></ul>
<h3 id="GitJob">GitJob
</h3>
<p>
</p>
<table>
<thead>
<tr>
<th>Field</th>
<th>Description</th>
</tr>
</thead>
<tbody>
<tr>
<td>
<code>apiVersion</code></br>
string</td>
<td>
<code>
gitjob.cattle.io/v1
</code>
</td>
</tr>
<tr>
<td>
<code>kind</code></br>
string
</td>
<td><code>GitJob</code></td>
</tr>
<tr>
<td>
<code>metadata</code></br>
<em>
<a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.13/#objectmeta-v1-meta">
Kubernetes meta/v1.ObjectMeta
</a>
</em>
</td>
<td>
Refer to the Kubernetes API documentation for the fields of the
<code>metadata</code> field.
</td>
</tr>
<tr>
<td>
<code>spec</code></br>
<em>
<a href="#gitjob.cattle.io/v1.GitJobSpec">
GitJobSpec
</a>
</em>
</td>
<td>
<br/>
<br/>
<table>
<tr>
<td>
<code>git</code></br>
<em>
<a href="#gitjob.cattle.io/v1.GitInfo">
GitInfo
</a>
</em>
</td>
<td>
<p>Git metadata information</p>
</td>
</tr>
<tr>
<td>
<code>jobSpec</code></br>
<em>
<a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.13/#jobspec-v1-batch">
Kubernetes batch/v1.JobSpec
</a>
</em>
</td>
<td>
<p>Job template applied to git commit</p>
</td>
</tr>
<tr>
<td>
<code>syncInterval</code></br>
<em>
int
</em>
</td>
<td>
<p>define interval(in seconds) for controller to sync repo and fetch commits</p>
</td>
</tr>
</table>
</td>
</tr>
<tr>
<td>
<code>status</code></br>
<em>
<a href="#gitjob.cattle.io/v1.GitJobStatus">
GitJobStatus
</a>
</em>
</td>
<td>
</td>
</tr>
</tbody>
</table>
<h3 id="Credential">Credential
</h3>
<p>
(<em>Appears on:</em>
<a href="#github.com%2francher%2fgitjob%2fpkg%2fapis%2fgitjob.cattle.io%2fv1.GitInfo">GitInfo</a>)
</p>
<p>
</p>
<table>
<thead>
<tr>
<th>Field</th>
<th>Description</th>
</tr>
</thead>
<tbody>
<tr>
<td>
<code>caBundle</code></br>
<em>
[]byte
</em>
</td>
<td>
<p>CABundle is a PEM encoded CA bundle which will be used to validate the repo&rsquo;s certificate.</p>
</td>
</tr>
<tr>
<td>
<code>insecureSkipTLSVerify</code></br>
<em>
bool
</em>
</td>
<td>
<p>InsecureSkipTLSverify will use insecure HTTPS to download the repo&rsquo;s index.</p>
</td>
</tr>
<tr>
<td>
<code>gitHostName</code></br>
<em>
string
</em>
</td>
<td>
<p>Hostname of git server</p>
</td>
</tr>
<tr>
<td>
<code>gitSecretName</code></br>
<em>
string
</em>
</td>
<td>
<p>Secret Name of git credential</p>
</td>
</tr>
</tbody>
</table>
<h3 id="GitEvent">GitEvent
</h3>
<p>
(<em>Appears on:</em>
<a href="#github.com%2francher%2fgitjob%2fpkg%2fapis%2fgitjob.cattle.io%2fv1.GitJobStatus">GitJobStatus</a>)
</p>
<p>
</p>
<table>
<thead>
<tr>
<th>Field</th>
<th>Description</th>
</tr>
</thead>
<tbody>
<tr>
<td>
<code>commit</code></br>
<em>
string
</em>
</td>
<td>
<p>The latest commit SHA received from git repo</p>
</td>
</tr>
<tr>
<td>
<code>lastExecutedCommit</code></br>
<em>
string
</em>
</td>
<td>
<p>Last executed commit by gitjob controller</p>
</td>
</tr>
<tr>
<td>
<code>GithubMeta</code></br>
<em>
<a href="#gitjob.cattle.io/v1.GithubMeta">
GithubMeta
</a>
</em>
</td>
<td>
</td>
</tr>
</tbody>
</table>
<h3 id="GitInfo">GitInfo
</h3>
<p>
(<em>Appears on:</em>
<a href="#github.com%2francher%2fgitjob%2fpkg%2fapis%2fgitjob.cattle.io%2fv1.GitJobSpec">GitJobSpec</a>)
</p>
<p>
</p>
<table>
<thead>
<tr>
<th>Field</th>
<th>Description</th>
</tr>
</thead>
<tbody>
<tr>
<td>
<code>Credential</code></br>
<em>
<a href="#gitjob.cattle.io/v1.Credential">
Credential
</a>
</em>
</td>
<td>
<p>Git credential metadata</p>
</td>
</tr>
<tr>
<td>
<code>provider</code></br>
<em>
string
</em>
</td>
<td>
<p>Git provider model to fetch commit. Can be polling(regular git fetch)/webhook(github webhook)</p>
</td>
</tr>
<tr>
<td>
<code>repo</code></br>
<em>
string
</em>
</td>
<td>
<p>Git repo URL</p>
</td>
</tr>
<tr>
<td>
<code>revision</code></br>
<em>
string
</em>
</td>
<td>
<p>Git commit. If specified, controller will use this SHA instead of auto-fetching commit</p>
</td>
</tr>
<tr>
<td>
<code>branch</code></br>
<em>
string
</em>
</td>
<td>
<p>Git branch. Default to master</p>
</td>
</tr>
<tr>
<td>
<code>Github</code></br>
<em>
<a href="#gitjob.cattle.io/v1.Github">
Github
</a>
</em>
</td>
<td>
</td>
</tr>
</tbody>
</table>
<h3 id="GitJobSpec">GitJobSpec
</h3>
<p>
(<em>Appears on:</em>
<a href="#github.com%2francher%2fgitjob%2fpkg%2fapis%2fgitjob.cattle.io%2fv1.GitJob">GitJob</a>)
</p>
<p>
</p>
<table>
<thead>
<tr>
<th>Field</th>
<th>Description</th>
</tr>
</thead>
<tbody>
<tr>
<td>
<code>git</code></br>
<em>
<a href="#gitjob.cattle.io/v1.GitInfo">
GitInfo
</a>
</em>
</td>
<td>
<p>Git metadata information</p>
</td>
</tr>
<tr>
<td>
<code>jobSpec</code></br>
<em>
<a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.13/#jobspec-v1-batch">
Kubernetes batch/v1.JobSpec
</a>
</em>
</td>
<td>
<p>Job template applied to git commit</p>
</td>
</tr>
<tr>
<td>
<code>syncInterval</code></br>
<em>
int
</em>
</td>
<td>
<p>define interval(in seconds) for controller to sync repo and fetch commits</p>
</td>
</tr>
</tbody>
</table>
<h3 id="GitJobStatus">GitJobStatus
</h3>
<p>
(<em>Appears on:</em>
<a href="#github.com%2francher%2fgitjob%2fpkg%2fapis%2fgitjob.cattle.io%2fv1.GitJob">GitJob</a>)
</p>
<p>
</p>
<table>
<thead>
<tr>
<th>Field</th>
<th>Description</th>
</tr>
</thead>
<tbody>
<tr>
<td>
<code>GitEvent</code></br>
<em>
<a href="#gitjob.cattle.io/v1.GitEvent">
GitEvent
</a>
</em>
</td>
<td>
</td>
</tr>
<tr>
<td>
<code>jobStatus</code></br>
<em>
string
</em>
</td>
<td>
<p>Status of job launched by controller</p>
</td>
</tr>
<tr>
<td>
<code>observedGeneration</code></br>
<em>
int64
</em>
</td>
<td>
<p>Generation of status to indicate if resource is out-of-sync</p>
</td>
</tr>
<tr>
<td>
<code>conditions</code></br>
<em>
[]github.com/rancher/wrangler/pkg/genericcondition.GenericCondition
</em>
</td>
<td>
<p>Condition of the resource</p>
</td>
</tr>
</tbody>
</table>
<h3 id="Github">Github
</h3>
<p>
(<em>Appears on:</em>
<a href="#github.com%2francher%2fgitjob%2fpkg%2fapis%2fgitjob.cattle.io%2fv1.GitInfo">GitInfo</a>)
</p>
<p>
</p>
<table>
<thead>
<tr>
<th>Field</th>
<th>Description</th>
</tr>
</thead>
<tbody>
<tr>
<td>
<code>secret</code></br>
<em>
string
</em>
</td>
<td>
<p>Secret Token used to validate requests to ensure only github requests is coming through</p>
</td>
</tr>
</tbody>
</table>
<h3 id="GithubMeta">GithubMeta
</h3>
<p>
(<em>Appears on:</em>
<a href="#github.com%2francher%2fgitjob%2fpkg%2fapis%2fgitjob.cattle.io%2fv1.GitEvent">GitEvent</a>)
</p>
<p>
</p>
<table>
<thead>
<tr>
<th>Field</th>
<th>Description</th>
</tr>
</thead>
<tbody>
<tr>
<td>
<code>hookId</code></br>
<em>
string
</em>
</td>
<td>
<p>Github webhook ID. Internal use only. This is to track</p>
</td>
</tr>
<tr>
<td>
<code>secretToken</code></br>
<em>
string
</em>
</td>
<td>
<p>Github webhook validation token to validate requests that are only coming from github</p>
</td>
</tr>
<tr>
<td>
<code>event</code></br>
<em>
string
</em>
</td>
<td>
<p>Last github webhook event</p>
</td>
</tr>
</tbody>
</table>
<hr/>
<p><em>
Generated with <code>gen-crd-api-reference-docs</code>
on git commit <code>9ae38a0</code>.
</em></p>>
<tr>
<th>Field</th>
<th>Description</th>
</tr>
</thead>
<tbody>
<tr>
<td>
<code>GitEvent</code></br>
<em>
<a href="#gitjob.cattle.io/v1.GitEvent">
GitEvent
</a>
</em>
</td>
<td>
</td>
</tr>
<tr>
<td>
<code>jobStatus</code></br>
<em>
string
</em>
</td>
<td>
<p>Status of job launched by controller</p>
</td>
</tr>
<tr>
<td>
<code>observedGeneration</code></br>
<em>
int64
</em>
</td>
<td>
<p>Generation of status to indicate if resource is out-of-sync</p>
</td>
</tr>
<tr>
<td>
<code>conditions</code></br>
<em>
[]github.com/rancher/wrangler/pkg/genericcondition.GenericCondition
</em>
</td>
<td>
<p>Condition of the resource</p>
</td>
</tr>
</tbody>
</table>
<h3 id="Github">Github
</h3>
<p>
(<em>Appears on:</em>
<a href="#github.com%2francher%2fgitjob%2fpkg%2fapis%2fgitjob.cattle.io%2fv1.GitInfo">GitInfo</a>)
</p>
<p>
</p>
<table>
<thead>
<tr>
<th>Field</th>
<th>Description</th>
</tr>
</thead>
<tbody>
<tr>
<td>
<code>secret</code></br>
<em>
string
</em>
</td>
<td>
<p>Secret Token used to validate requests to ensure only github requests is coming through</p>
</td>
</tr>
</tbody>
</table>
<h3 id="GithubMeta">GithubMeta
</h3>
<p>
(<em>Appears on:</em>
<a href="#github.com%2francher%2fgitjob%2fpkg%2fapis%2fgitjob.cattle.io%2fv1.GitEvent">GitEvent</a>)
</p>
<p>
</p>
<table>
<thead>
<tr>
<th>Field</th>
<th>Description</th>
</tr>
</thead>
<tbody>
<tr>
<td>
<code>hookId</code></br>
<em>
string
</em>
</td>
<td>
<p>Github webhook ID. Internal use only. If not empty, means a webhook is created along with this CR</p>
</td>
</tr>
<tr>
<td>
<code>secretToken</code></br>
<em>
string
</em>
</td>
<td>
<p>Github webhook validation token to validate requests that are only coming from github</p>
</td>
</tr>
<tr>
<td>
<code>event</code></br>
<em>
string
</em>
</td>
<td>
<p>Last received github webhook event</p>
</td>
</tr>
</tbody>
</table>
<hr/>
<p><em>
Generated with <code>gen-crd-api-reference-docs</code>
on git commit <code>9ae38a0</code>.
</em></p>



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
