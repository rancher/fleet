# Webhook

By default, Fleet utilizes polling (default: 15 seconds) to pull from a Git repo.However, this can be configured to utilize a webhook instead.Fleet currently supports Github,
GitLab, Bitbucket, Bitbucket Server and Gogs.

### 1. Configure the webhook service. Fleet uses `gitjob` service to handle webhook requests. Create an ingress that points to `gitjob` service.

```yaml
apiVersion: networking.k8s.io/v1beta1
kind: Ingress
metadata:
  name: webhook-ingress
  namespace: fleet-system
spec:
  rules:
  - host: your.domain.com
    http:
      paths:
        - path: /
          pathType: Prefix
          backend:
            serviceName: gitjob
            servicePort: 80
```

!!! note
    You can configure [TLS](https://kubernetes.io/docs/concepts/services-networking/ingress/#tls) on ingress. 

### 2. Go to your webhook provider and configure the webhook callback url. Here is a Github example.

![](./assets/webhook.png)

Configuring secret is optional. This is used to validate webhook payload as payload should not be trusted by default. 
If your webhook server is public accessible to internet then it is recommended to configure. If you do configure the 
secret, follow step 3.

!!! note 
    only application/json is supported due to the limitation of webhook library.

!!! note
    If you configured the webhook the polling interval will be automatically adjusted to 1 hour.
    
### 3. (Optional) Configure webhook secret. The secret is for validating webhook payload. Make sure to put it in a k8s secret called `gitjob-webhook` in `fleet-system`.

| Provider        | K8s Secret Key                   |
|-----------------| ---------------------------------|
| GitHub          | `github`                         |
| GitLab          | `gitlab`                         |
| BitBucket       | `bitbucket`                      |
| BitBucketServer | `bitbucket-server`               |
| Gogs            | `gogs`                           |

For example, to create a secret containing github secret to validate webhook payload, run

```shell
kubectl create secret generic gitjob-webhook -n fleet-system --from-literal=github=webhooksecretvalue
```