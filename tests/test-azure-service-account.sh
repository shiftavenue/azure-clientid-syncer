#! /bin/bash -e
set -x

# read output.json file and set variables
source $(realpath $(dirname "$0"))/tests.env
az aks get-credentials --resource-group $RG --name $CLUSTER

TEST_IDENTITY_NAME=testsa

TEST_IDENTITY_CLIENT_ID="$(az identity create \
  --resource-group $RG \
  --name $TEST_IDENTITY_NAME \
  --query clientId -otsv)"

az identity federated-credential create \
  --identity-name $TEST_IDENTITY_NAME \
  --name testsa \
  --resource-group $RG \
  --audiences api://AzureADTokenExchange \
  --issuer $ISSUER \
  --subject system:serviceaccount:default:testsa

sleep 10

for run in {1..10}; do
  az identity list --resource-group $RG | grep $TEST_IDENTITY_CLIENT_ID
  if [[ $? -eq 0 ]]; then
    echo "identity found in list"
    break
  fi
  echo "waiting for identity to appear in list"
  sleep 6
done

az role assignment create \
  --role "Key Vault Secrets User" \
  --assignee $TEST_IDENTITY_CLIENT_ID \
  --scope "$KV_ID"

kubectl wait pod --all --for=condition=Ready --namespace=$NAMESPACE --timeout=60s

cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ServiceAccount
metadata:
  labels:
    azure.clientid.syncer/use: "true"
  name: testsa
---
apiVersion: v1
kind: Pod
metadata:
  name: quick-start
  labels:
    azure.workload.identity/use: "true"
spec:
  serviceAccountName: testsa
  containers:
  - image: ghcr.io/azure/azure-workload-identity/msal-go
    name: oidc
    env:
    - name: KEYVAULT_URL
      value: $VAULT_URI
    - name: SECRET_NAME
      value: test
  nodeSelector:
    kubernetes.io/os: linux
EOF

kubectl wait --for=condition=Ready pod/quick-start --timeout=60s

if [[ $(kubectl get sa -ojson testsa | jq -r '.metadata.annotations."azure.workload.identity/client-id"') != $TEST_IDENTITY_CLIENT_ID ]]; then
  echo "Service account testsa does not have the correct client id"
  exit 1
fi

if [[ $(kubectl get sa -ojson testsa | jq -r '.metadata.annotations."azure.workload.identity/tenant-id"') != $TENANT_ID ]]; then
  echo "Service account testsa does not have the correct tenant id"
  exit 1
fi

for run in {1..10}; do
  if [[ ! -z $(kubectl logs quick-start) ]]; then
    break
  fi
  echo "waiting for secret to appear in logs"
  sleep 6
  if [[ $run -eq 10 ]]; then
    echo "No logs found in pod. Exiting."
    exit 1
  fi
done

if [[ $(kubectl logs quick-start) != *"$SECRET_VALUE"* ]]; then
  echo "Secret not found in pod. Latest logs are:"
  kubectl logs quick-start
  exit 1
else
  echo "Secret found in pod"
fi

set +x
echo "###################################################"
echo "######## succeeded azure service account ##########"
echo "###################################################"