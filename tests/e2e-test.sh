#! /bin/bash -e
set -x

rand=$(tr -dc a-z </dev/urandom | head -c 4 ; echo '')

resource_group="azure-clientid-syncer-$rand"
location=westeurope
cluster="aks-azure-clientid-syncer-$rand"
identity_name="azure-clientid-syncer-$rand"
key_vault="azclientsyncer$rand"
delete_resources_afterwards="true"
no_wait_on_delete="true"

while getopts g:l:c:i:k:d:n: flag; do
  case "${flag}" in
  g) resource_group=${OPTARG} ;;
  l) location=${OPTARG} ;;
  c) cluster=${OPTARG} ;;
  i) identity_name=${OPTARG} ;;
  k) key_vault=${OPTARG} ;;
  d) delete_resources_afterwards=${OPTARG} ;;
  n) no_wait_on_delete=${OPTARG} ;;
  esac
done

RG=$resource_group
CLUSTER=$cluster
LOCATION=$location
IDENTITY_NAME=$identity_name
KV_NAME=$key_vault
SUBSCRIPTION_ID=$(az account show --query id -otsv)
TENANT_ID=$(az account show --query tenantId -otsv)
TEST_IDENTITY_NAME=testsa
SECRET_VALUE="Hello"

function cleanup {
  echo "cleaning up azure rg $RG ..."
  if [ "$no_wait_on_delete" = "true" ]; then
    az group delete -n $RG --no-wait --yes
    echo "cleaning up azure rg $RG in the background"
  else
    az group delete -n $RG --yes
    echo "cleaned up azure rg $RG"
  fi
}

if [ "$delete_resources_afterwards" = "true" ]; then
  trap cleanup EXIT
fi

az group create -l $LOCATION -n $RG

IDENTITY_CLIENT_ID="$(az identity create \
  --resource-group $RG \
  --name $IDENTITY_NAME \
  --query clientId -otsv)"

TEST_IDENTITY_CLIENT_ID="$(az identity create \
  --resource-group $RG \
  --name $TEST_IDENTITY_NAME \
  --query clientId -otsv)"

az aks create \
  --resource-group $RG \
  --name $CLUSTER \
  --node-count 1 \
  --enable-oidc-issuer \
  --enable-workload-identity \
  --generate-ssh-keys

ISSUER="$(az aks show --resource-group $RG --name $CLUSTER --query "oidcIssuerProfile.issuerUrl" -otsv)"

az identity federated-credential create \
  --identity-name $IDENTITY_NAME \
  --name test-azure-clientid-syncer \
  --resource-group $RG \
  --audiences api://AzureADTokenExchange \
  --issuer $ISSUER \
  --subject system:serviceaccount:azure-clientid-syncer-system:azure-clientid-syncer-webhook-admin

az role assignment create \
  --role Reader \
  --assignee $IDENTITY_CLIENT_ID \
  --scope subscriptions/$SUBSCRIPTION_ID

az aks get-credentials --resource-group $RG --name $CLUSTER

cat <<EOF >values.yaml
config:
  azureTenantID: "$TENANT_ID"
  # filterTags: "aks-clientid-syncer:true,namespace:<NAMESPACE>,serviceaccountname:<SERVICE_ACCOUNT_NAME>"
podLabels:
  azure.workload.identity/use: "true"
serviceAccount:
  annotations:
    azure.workload.identity/client-id: "$IDENTITY_CLIENT_ID"
    azure.workload.identity/tenant-id: "$TENANT_ID"
EOF

NAMESPACE=azure-clientid-syncer-system
helm repo add azure-clientid-syncer https://shiftavenue.github.io/azure-clientid-syncer
helm install clientid-syncer-webhook azure-clientid-syncer/azure-clientid-syncer-webhook \
  --namespace $NAMESPACE \
  --create-namespace \
  -f values.yaml
rm values.yaml

KV_ID="$(az keyvault create \
  --resource-group $RG \
  --location $LOCATION \
  --name $KV_NAME \
  --enable-rbac-authorization true \
  --query id \
  -otsv)"

az role assignment create \
  --role "Key Vault Secrets Officer" \
  --assignee $(az ad signed-in-user show --query id -otsv) \
  --scope "$KV_ID"

az keyvault secret set \
  --vault-name "$KV_NAME" \
  --name "test" \
  --value "$SECRET_VALUE"

az identity federated-credential create \
  --identity-name $TEST_IDENTITY_NAME \
  --name testsa \
  --resource-group $RG \
  --audiences api://AzureADTokenExchange \
  --issuer $ISSUER \
  --subject system:serviceaccount:default:testsa

az role assignment create \
  --role "Key Vault Secrets User" \
  --assignee $TEST_IDENTITY_CLIENT_ID \
  --scope "$KV_ID"

VAULT_URI="$(az keyvault show -g $RG --name $KV_NAME --query properties.vaultUri -otsv)"

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

echo "e2e test successful"
cleanup