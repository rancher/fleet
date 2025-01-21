#!/bin/bash

dir=${1-'FleetCI-RootCA'}

# Create secret with certs, needed by test git server
kubectl -n fleet-local create secret generic git-server-certs \
    --from-file=./"$dir"/helm.crt \
    --from-file=./"$dir"/helm.key

# Create cattle-system namespace
kubectl create ns cattle-system

# Create Rancher CA bundle secret
kubectl -n cattle-system create secret generic tls-ca-additional --from-file=ca-additional.pem=./"$dir"/root.crt
