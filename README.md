# Azure ClientId Syncer
This webhook syncs federated identity credentials from Azure for a Kubernetes cluster. Every time a Kubernetes service account with a specific label gets created it queries the Azure Managed Identities to fetch the client ID and tenant ID, and patches these values into this service account.

## Prerequirements
- TenantID should be either exported or added manually during the installation, like:
```export AZURE_TENANT_ID="$(az account show -s <AzureSubscriptionID> --query tenantId -otsv)"```
- Make sure you followed the [Azure AD Workload Identity Instructions](https://azure.github.io/azure-workload-identity/docs/installation.html)

## Installation
```
helm repo add azure-clientid-syncer https://shiftavenue.github.io/azure-workload-identity/charts
helm repo update
helm install clientid-syncer-webhook azure-clientid-syncer/clientid-syncer-webhook \
   --namespace azure-clientid-syncer-system \
   --create-namespace \
   --set azureTenantID="${AZURE_TENANT_ID}"
```