#!/bin/bash

echo "Creating certificates"
mkdir certs
openssl genrsa -out certs/tls.key 2048
openssl req -new -key certs/tls.key -out certs/tls.csr -subj "/CN=webhook-server.production.svc"
openssl x509 -req -extfile <(printf "subjectAltName=DNS:webhook-server.production.svc") -in certs/tls.csr -signkey certs/tls.key -out certs/tls.crt

echo "Creating Webhook Server TLS Secret"
kubectl create secret tls webhook-server-tls \
    --cert "certs/tls.crt" \
    --key "certs/tls.key" -n production

echo "Creating Webhook Server Deployment"
kubectl create -f manifests/webhook_server.yml -n production

echo "Creating K8s Webhooks"
ENCODED_CA=$(cat certs/tls.crt | base64 | tr -d '\n')
sed -e 's@${ENCODED_CA}@'"$ENCODED_CA"'@g' <"manifests/webhooks.yml" | kubectl create -f -