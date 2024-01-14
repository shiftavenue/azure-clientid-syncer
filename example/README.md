1. Deploy your AKS cluster or update your AKS:
    1. Deploy new AKS cluster with OIDC and Workload Identity enabled:
    ```bash
    az aks create --resource-group <resource-group> --name <cluster-name> --node-count 1 --enable-oidc-issuer --enable-workload-identity
    ISSUER=$(az aks show --resource-group test-group --name awdawd --query "oidcIssuerProfile.issuerUrl" -otsv)
    ```
    2. Update existing AKS cluster with OIDC and Workload Identity enabled:
    ```bash
    az aks update --resource-group <resource-group> --name <cluster-name> --enable-oidc-issuer --enable-workload-identity
    ```
2. Create a managed identity for your application with the federated identity for the azure-clientid-syncer:
    ```bash
    az identity create --resource-group <resource-group> --name <identity-name>
    az identity federated-credential create --identity-name <identity-name>
                                        --name <fed-identity-name>
                                        --resource-group <resource-group>
                                        --audiences api://AzureADTokenExchange
                                        --issuer $ISSUER
                                        --subject system:serviceaccount:azure-clientid-syncer-system:azure-clientid-syncer-webhook-admin
    ```
3. Assign Reader permissions to your managed identity:
    ```bash
    az role assignment create --role Reader --assignee <identity-client-id> --scope subscriptions/<subscription-id>
    ```
