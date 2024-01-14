# Azure ClientId Syncer
This webhook syncs federated identity credentials from Azure for a Kubernetes cluster. Every time a Kubernetes service account with a specific label gets created it queries the Azure Managed Identities to fetch the client ID and tenant ID, and patches these values into this service account.

## Prerequirements
- TenantID should be either exported or added manually during the installation, like:
```export AZURE_TENANT_ID="$(az account show -s <AzureSubscriptionID> --query tenantId -otsv)"```
- Make sure you followed the [Azure AD Workload Identity Instructions](https://azure.github.io/azure-workload-identity/docs/installation.html)

## Installation
> **_NOTE:_**  The following installation is not using extra values to configure Workload Identity or other Authentication mechanisms. Check the **Getting started** section for more information.
```
helm repo add azure-clientid-syncer https://shiftavenue.github.io/azure-clientid-syncer
helm repo update
helm install clientid-syncer-webhook azure-clientid-syncer/azure-clientid-syncer-webhook \
   --namespace azure-clientid-syncer-system \
   --create-namespace \
   --set config.azureTenantID="${AZURE_TENANT_ID}"
```

## Getting started 
1. Create a managed identity with an federated identity credential to use azure-client-syncer with Workload Identity - configure the credential according to your environment. The following are the default values for the Service Account deployed with the chart:
* Namespace: azure-clientid-syncer-system
* Name: azure-clientid-syncer-webhook-admin
2. Assign Reader permissions to your managed identity:
3. Install the helm chart with the values according to your managed identity and tenant. (An example can be found [here](example/example-values.yaml))
4. Start deploying...

## Performance considerations
The webhook is called every time a service account is created. This can lead to a lot of calls to the Azure API required to check the federated identity credentials. To reduce the number of calls, the webhook allows to set a **FILTER_TAGS** environment variable and you should follow the principal of priviledge when assigning Reader permissions to the identity. This variable contains a comma separated list of tags which will be used as additional parameter for the query of the Azure managed identities. Kubernetes mutation webhooks have a max. timeout of 30 seconds. To achieve this time it is recommended to build a query which returns at **maximum around ~70 managed identities**.
