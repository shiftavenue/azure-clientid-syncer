1. Deploy your AKS cluster or update your AKS:
    1. Deploy new AKS cluster with OIDC and Workload Identity enabled:
    ```bash
    RG=test-azure-clientid-syncer
    CLUSTER=test-aks-azure-clientid-syncer
    LOCATION=westeurope
    az group create -l $LOCATION -n $RG
    az aks create --resource-group $RG --name $CLUSTER --node-count 1 --enable-oidc-issuer --enable-workload-identity
    ISSUER=$(az aks show --resource-group $RG --name $CLUSTER --query "oidcIssuerProfile.issuerUrl" -otsv)
    ```
    2. Update existing AKS cluster with OIDC and Workload Identity enabled:
    ```bash
    RG=<your-resource-group>
    CLUSTER=<your-cluster-name>
    az aks update --resource-group $RG --name $CLUSTER --enable-oidc-issuer --enable-workload-identity
    ```
2. Create a managed identity for your application with the federated identity for the azure-clientid-syncer:
    ```bash
    IDENTITY_NAME=test-azure-clientid-syncer
    IDENTITY_CLIENT_ID=$(az identity create --resource-group $RG --name $IDENTITY_NAME --query clientId -otsv)
    az identity federated-credential create --identity-name $IDENTITY_NAME \
                                        --name test-azure-clientid-syncer \
                                        --resource-group $RG \
                                        --audiences api://AzureADTokenExchange \
                                        --issuer $ISSUER \
                                        --subject system:serviceaccount:azure-clientid-syncer-system:azure-clientid-syncer-webhook-admin
    ```
3. Assign Reader permissions to your managed identity:
    ```bash
    SUBSCRIPTION_ID=$(az account show --query id -otsv)
    az role assignment create --role Reader --assignee $IDENTITY_CLIENT_ID --scope subscriptions/$SUBSCRIPTION_ID
    ```
4. Get the credentials for your cluster:
    ```bash
    az aks get-credentials --resource-group $RG --name $CLUSTER
    ```
5. (Optional) Create demo resources:
    ```bash
    KV_NAME=test-kv-azure-clientid
    KV_ID=$(az keyvault create --resource-group $RG --location $LOCATION --name $KV_NAME --enable-rbac-authorization true --query id -otsv)
    az role assignment create --role "Key Vault Secrets Officer"  --assignee $(az ad signed-in-user show --query id -otsv) --scope "$KV_ID"
    az keyvault secret set --vault-name "$KV_NAME" --name "test" --value 'Hello!'

    TEST_IDENTITY_NAME=testsa
    TEST_IDENTITY_CLIENT_ID=$(az identity create --resource-group $RG --name $TEST_IDENTITY_NAME --query clientId -otsv)
    az identity federated-credential create --identity-name $TEST_IDENTITY_NAME \
                                        --name testsa \
                                        --resource-group $RG \
                                        --audiences api://AzureADTokenExchange \
                                        --issuer $ISSUER \
                                        --subject system:serviceaccount:default:testsa
    az role assignment create --role "Key Vault Secrets User"  --assignee $TEST_IDENTITY_CLIENT_ID --scope "$KV_ID"
    ```
5. Install the helm chart with the values according to your managed identity and tenant. (An example can be found [here](example-values.yaml) and the general instraction are [here](../README.md#installation))
6. Test it:
    ```bash
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
            value: $(az keyvault show --name $KV_NAME --query properties.vaultUri -otsv)
          - name: SECRET_NAME
            value: test
        nodeSelector:
          kubernetes.io/os: linux
    EOF

    kubectl logs quick-start
    ```