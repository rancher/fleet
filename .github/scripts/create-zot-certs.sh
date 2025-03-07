#!/bin/bash

CA_name=${1-'FleetCI-RootCA'}
validity_days=365
passphrase=foo # this should not matter too much for local, ephemeral certs and keys

mkdir -p "$CA_name"
cd "$CA_name"

must_create_certs ()
{
    if [ -z "$(find . -iname '*.crt')" ]; then
        return 1
    fi

    age_of_most_recent_cert=$(( $(date +%s) - $(date -r $(ls -1 -t *.crt | head -n 1) +%s) ))

    [ $(( $age_of_most_recent_crt )) -lt $(( validity_days * 24 * 3600 )) ]

    return $?
}

must_create_certs
if [ $? -eq 1 ]; then
    # generate private key for CA
    CA_key_name="$CA_name.key"
    openssl genrsa -aes256 -passout pass:$passphrase -out "$CA_key_name" 4096

    # generate CA cert using private key
    openssl req -x509 -new -nodes -key "$CA_key_name" -sha256 -days $validity_days -subj "/C=DE/ST=Fleetland/L=Fleetcity/O=Rancher/OU=Fleet/CN=Fleet-Test Root CA" -passin pass:$passphrase -out root.crt

    # generate Zot cert and key
    openssl req -new -newkey rsa:4096 -sha256 -nodes -subj "/C=DE/ST=Fleetland/L=Fleetcity/O=Rancher/OU=Fleet/CN=Fleet-Test" -out helm.csr -keyout helm.key

    cat > crt.ext << EOF
    authorityKeyIdentifier=keyid,issuer
    basicConstraints=CA:FALSE
    keyUsage = digitalSignature, nonRepudiation, keyEncipherment, dataEncipherment
    subjectAltName = @alt_names
    [alt_names]
    DNS.1 = zot-service.default.svc.cluster.local
    DNS.2 = chartmuseum-service.default.svc.cluster.local
    DNS.3 = git-service.default.svc.cluster.local
EOF

    # sign Zot cert with CA root key
    openssl x509 -req -in helm.csr -CA root.crt -CAkey "$CA_key_name" -CAcreateserial -out helm.crt -days $validity_days -extfile crt.ext -passin pass:$passphrase
else
    echo "Skipping creation of certificates, as certificates more recent than $validity_days days exist."
fi
