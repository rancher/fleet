# Troubleshooting

This is a collection of information, which help you to troubleshoot if fleet is not working

## Check status of fleet controller

You can check the status of the Fleet controller pods by running the below commands.

```bash
kubectl -n fleet-system logs -l app=fleet-controller
kubectl -n fleet-system get pods -l app=fleet-controller
```

```bash
NAME                                READY   STATUS    RESTARTS   AGE
fleet-controller-64f49d756b-n57wq   1/1     Running   0          3m21s
```

## Fleet fails with bad response code: 403

If you see your GitJob complaining with this error

```
time="2021-11-04T09:21:24Z" level=fatal msg="bad response code: 403"
```

This may indicate that fleet cannot access the helm repo you specified in your [`fleet.yaml`](./gitrepo-structure.md).

- check if your repo is accessible from your dev machine and you can actually download the helm chart
- check if your credentials for the git repo are fine

## Helm chart repo: certificate signed by unknown authority

If you see your GitJob complaining with this error

```
time="2021-11-11T05:55:08Z" level=fatal msg="Get \"https://helm.intra/virtual-helm/index.yaml\": x509: certificate signed by unknown authority" 
```

You may have added the wrong certificate chain. Please verify with

```bash
context=playground-local
kubectl get secret -n fleet-default helm-repo -o jsonpath="{['data']['cacerts']}" --context $context | base64 -d | openssl x509 -text -noout
Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number:
            7a:1e:df:79:5f:b0:e0:be:49:de:11:5e:d9:9c:a9:71
        Signature Algorithm: sha512WithRSAEncryption
        Issuer: C = CH, O = MY COMPANY, CN = NOP Root CA G3
...

```
