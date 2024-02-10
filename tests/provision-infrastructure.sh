#! /bin/bash -e
set -x

RAND=""
# check if .env file exists and source it or create new rand
if [ -f $(realpath $(dirname "$0"))/../.env ]; then
  source $(realpath $(dirname "$0"))/../.env
else
  RAND=$(tr -dc a-z </dev/urandom | head -c 4 && echo)
fi

AZURE_CLIENT_ID_SYNCER_VERSION=$1
OWN_IDENTITY_CLIENT_ID=$2
RG="azure-clientid-syncer-$RAND"
CLUSTER="aks-azure-clientid-syncer-$RAND"
LOCATION="westeurope"
IDENTITY_NAME="azure-clientid-syncer-$RAND"
KV_NAME="azclientsyncer$RAND"
SUBSCRIPTION_ID=$(az account show --query id -otsv)
TENANT_ID=$(az account show --query tenantId -otsv)
SECRET_VALUE="Hello"


if [[ -z $OWN_IDENTITY_CLIENT_ID ]]; then
  echo "OWN_IDENTITY_CLIENT_ID is not set. Using the signed-in user."
  OWN_IDENTITY_TYPE="User"
  OWN_IDENTITY_OBJECT_ID=$(az ad signed-in-user show --query id -otsv)
else
  OWN_IDENTITY_TYPE="ServicePrincipal"
  OWN_IDENTITY_OBJECT_ID=$(az ad sp show --id $OWN_IDENTITY_CLIENT_ID --query id -otsv)
fi

az group create -l $LOCATION -n $RG

az aks create \
  --resource-group $RG \
  --name $CLUSTER \
  --node-count 1 \
  --enable-oidc-issuer \
  --enable-workload-identity \
  --generate-ssh-keys \
  --no-wait

az keyvault create \
  --resource-group $RG \
  --location $LOCATION \
  --name $KV_NAME \
  --enable-rbac-authorization true \
  --no-wait

IDENTITY_CLIENT_ID="$(az identity create \
  --resource-group $RG \
  --name $IDENTITY_NAME \
  --query clientId -otsv)"

IDENTITY_PRINCIPAL_ID="$(az identity show \
  --resource-group $RG \
  --name $IDENTITY_NAME \
  --query principalId -otsv)"

az keyvault wait --resource-group $RG --name $KV_NAME --created

KV_ID="$(az keyvault show \
  --name $KV_NAME \
  --query id \
  -otsv)"

az role assignment create \
  --role "Key Vault Secrets Officer" \
  --assignee-object-id $OWN_IDENTITY_OBJECT_ID \
  --assignee-principal-type $OWN_IDENTITY_TYPE \
  --scope "$KV_ID"

az aks wait --resource-group $RG --name $CLUSTER --created

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
  --assignee-object-id $IDENTITY_PRINCIPAL_ID \
  --assignee-principal-type ServicePrincipal \
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
  release: "$AZURE_CLIENT_ID_SYNCER_VERSION"
EOF
fi

NAMESPACE=azure-clientid-syncer-system
helm repo add azure-clientid-syncer https://shiftavenue.github.io/azure-clientid-syncer
helm install clientid-syncer-webhook azure-clientid-syncer/azure-clientid-syncer-webhook \
  --namespace $NAMESPACE \
  --create-namespace \
  -f values.yaml
rm values.yaml



az keyvault secret set \
  --vault-name "$KV_NAME" \
  --name "test" \
  --value "$SECRET_VALUE"

VAULT_URI="$(az keyvault show -g $RG --name $KV_NAME --query properties.vaultUri -otsv)"

cat <<EOF >>$(realpath $(dirname "$0"))/../.env
VAULT_URI=$VAULT_URI
KV_ID=$KV_ID
SECRET_VALUE=$SECRET_VALUE
TENANT_ID=$TENANT_ID
NAMESPACE=$NAMESPACE
CLUSTER=$CLUSTER
RAND=$RAND
ISSUER=$ISSUER
RG=$RG
EOF

set +x
echo "###################################################"
echo "########### provisioned infrastructure ############"
echo "###################################################"
