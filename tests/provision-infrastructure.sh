#! /bin/bash -e
set -x

RAND=$(tr -dc a-z </dev/urandom | head -c 4 && echo)

AZURE_CLIENT_ID_SYNCER_VERSION=$1
RG="azure-clientid-syncer-$RAND"
CLUSTER="aks-azure-clientid-syncer-$RAND"
LOCATION="westeurope"
IDENTITY_NAME="azure-clientid-syncer-$RAND"
KV_NAME="azclientsyncer$RAND"
SUBSCRIPTION_ID=$(az account show --query id -otsv)
TENANT_ID=$(az account show --query tenantId -otsv)
SECRET_VALUE="Hello"

az group create -l $LOCATION -n $RG

IDENTITY_CLIENT_ID="$(az identity create \
  --resource-group $RG \
  --name $IDENTITY_NAME \
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

if [ ! -z "$AZURE_CLIENT_ID_SYNCER_VERSION" ]; then
  echo "AZURE_CLIENT_ID_SYNCER_VERSION is set to $AZURE_CLIENT_ID_SYNCER_VERSION. Overriding the default version in the chart."
  cat <<EOF >>values.yaml
image:
  release: $AZURE_CLIENT_ID_SYNCER_VERSION
EOF
fi

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

az keyvault secret set \
  --vault-name "$KV_NAME" \
  --name "test" \
  --value "$SECRET_VALUE"

az role assignment create \
  --role "Key Vault Secrets Officer" \
  --assignee $(az ad signed-in-user show --query id -otsv) \
  --scope "$KV_ID"

VAULT_URI="$(az keyvault show -g $RG --name $KV_NAME --query properties.vaultUri -otsv)"

cat <<EOF >>$(realpath $(dirname "$0"))/tests.env
VAULT_URI=$VAULT_URI
KV_ID=$KV_ID
SECRET_VALUE=$SECRET_VALUE
TENANT_ID=$TENANT_ID
NAMESPACE=$NAMESPACE
RG=$RG
CLUSTER=$CLUSTER
RAND=$RAND
ISSUER=$ISSUER
EOF

set +x
echo "###################################################"
echo "########### provisioned infrastructure ############"
echo "###################################################"
