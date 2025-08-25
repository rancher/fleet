#!/bin/bash

set -euxo pipefail

# wait for Rancher to create the ingress before waiting for the loadbalancer
while ! kubectl get ingress -n cattle-system rancher; do
  sleep 1
done

# wait for loadBalancer IPs
echo "Waiting for loadbalancer IP to be assigned..."
timeout 300 bash -c 'until kubectl get ingress -n cattle-system rancher -o "go-template={{range .status.loadBalancer.ingress}}{{.ip}}{{\"\\n\"}}{{end}}" 2>/dev/null | grep -q ".*"; do sleep 2; done'

# wait for certificate
echo "Waiting for TLS certificate to be ready..."
timeout 300 bash -c 'until kubectl get certs -n cattle-system 2>/dev/null | grep -q "tls-rancher-ingress.*True"; do sleep 2; done'