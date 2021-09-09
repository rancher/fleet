# Secrets

Powered by [vals](https://github.com/variantdev/vals) you can also set up a secrets backend to source the secrets from.

## Configuration

Vals supports multiple [backends](https://github.com/variantdev/vals#suported-backends). Depending on the one you use you will need to set up a different environment variables. The example below use Hashicorp Vault:

```sh
NAMESPACE=${1:-fleet-system}
kubectl create secret generic \
  fleet-vals \
  --dry-run \
  --from-literal=VAULT_ADDR=${VAULT_ADDR} \
  --from-literal=VAULT_TOKEN=${VAULT_TOKEN} \
  --dry-run -oyaml | kubectl apply -n ${NAMESPACE} -f -
```

The secret can be either *upstream*, *downstream* or both. For example, if you Fleet from Rancher and only the downstream cluster has access to Vault you will need to set up the secret in the downstream cluster in the namespace `cattle-fleet-system` whilst if Vault is only available upstream the secret would be set up in the namespace `fleet-system`

If you don't need to use secrets, you can use environment variables instead. See below.

## Installing

* `agentSecret`: use this for downstream clusters
* `secret`: this secret is for upstream clusters

```shell
helm -n fleet-system install --create-namespace --wait \
    fleet-crd https://github.com/rancher/fleet/releases/download/{{fleet.version}}/fleet-crd-{{fleet.helmversion}}.tgz
helm -n fleet-system install --create-namespace --wait \
    --set secret=fleet-vals \
    fleet https://github.com/rancher/fleet/releases/download/{{fleet.version}}/fleet-{{fleet.helmversion}}.tgz
```

or using environment variables:

```sh
helm -n fleet-system install --create-namespace --wait \
    --set env[0]=VAULT_ADDR=${VAULT_ADDR} \
    --set env[1]=VAULT_TOKEN=${VAULT_TOKEN} \
    fleet https://github.com/rancher/fleet/releases/download/{{fleet.version}}/fleet-{{fleet.helmversion}}.tgz
```


## Using

The secret values can be set in either `values` or any of the files referenced by `valuesFiles`:

```yaml
namespace: sample-helm

helm:
  releaseName: httpbin
  chart: "github.com/twingao/httpbin"
  repo: ""
  version: "master"

  # Custom values that will be passed as values.yaml to the installation
  values:
    username: ref+vault://secret/helm/test/username
    password: ref+vault://secret/helm/test/password
  valuesFiles:
    - secrets.yaml
```