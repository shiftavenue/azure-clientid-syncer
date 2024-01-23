# Azure ClientId Syncer
This webhook syncs federated identity credentials from Azure for a Kubernetes cluster. Every time a Kubernetes service account with a specific label gets created it queries the Azure Managed Identities to fetch the client ID and tenant ID, and patches these values into this service account.

## Prerequirements
* An existing Azure subscription
* User with sufficient privileges to delegate the reader role on the required scope (where the managed identities for the Workload Identities are created in)
* [Azure CLI](https://learn.microsoft.com/en-us/cli/azure/install-azure-cli), [helm](https://helm.sh/docs/intro/install/) and [kubectl](https://kubernetes.io/docs/tasks/tools/#kubectl)


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
> **_NOTE:_**  To get a step-by-step guide check out the [example setup](example/README.md), which provides you a simple template of commands to set it up on your own.
1. Create a managed identity with an federated identity credential to use azure-client-syncer with Workload Identity - configure the credential according to your environment. The following are the default values for the Service Account deployed with the chart:
* Namespace: azure-clientid-syncer-system
* Name: azure-clientid-syncer-webhook-admin
2. Assign Reader permissions to your managed identity:
3. Install the helm chart with the values according to your managed identity and tenant. (An example can be found [here](example/example-values.yaml))
4. Start deploying...

## Performance considerations
The webhook is called every time a service account is created. This can lead to a lot of calls to the Azure API required to check the federated identity credentials. To reduce the number of calls, the webhook allows to set a **FILTER_TAGS** environment variable and you should follow the principal of priviledge when assigning Reader permissions to the identity. This variable contains a comma separated list of tags which will be used as additional parameter for the query of the Azure managed identities. Kubernetes mutation webhooks have a max. timeout of 30 seconds. To achieve this time it is recommended to build a query which returns at **maximum around ~70 managed identities**.
