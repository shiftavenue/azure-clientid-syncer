package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	arg "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/go-logr/logr"
	"github.com/shiftavenue/azure-clientid-syncer/pkg/config"
	corev1 "k8s.io/api/core/v1"
)

type azureQueryProvider struct {
	defaultQueryProvider
	Subscriptions                SubscriptionList
	cred                         *azidentity.DefaultAzureCredential
	serviceAccountQueryParameter serviceAccountQueryParameter
}

func NewAzureQueryProvider(logger logr.Logger, config config.Config) (*azureQueryProvider, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		logger.Error(err, "failed to obtain a credential")
		return nil, err
	}
	subscriptionList, err := retrieveCurrentSubscriptionList()
	if err != nil {
		logger.Error(err, "failed to retrieve current subscription list")
		return nil, err
	}
	return &azureQueryProvider{
		defaultQueryProvider: defaultQueryProvider{
			Logger: logger,
			config: config,
		},
		cred: cred,
		Subscriptions: *subscriptionList,
	}, nil
}

func (a *azureQueryProvider) Query(serviceAccount corev1.ServiceAccount) (*corev1.ServiceAccount, error) {
	a.serviceAccountQueryParameter = serviceAccountQueryParameter{
		serviceAccountName:      serviceAccount.Name,
		serviceAccountNamespace: serviceAccount.Namespace,
	}

	a.Logger.Info("identified service account with name: " + a.serviceAccountQueryParameter.serviceAccountName + " and namespace: " + a.serviceAccountQueryParameter.serviceAccountNamespace)

	clientid, err := a.searchForClientIdInSubscriptions()

	if err == nil {
		a.Logger.Info("Setting new annotations for service account", "name", a.serviceAccountQueryParameter.serviceAccountName, "namespace", a.serviceAccountQueryParameter.serviceAccountNamespace, azureClientidAnnotation, clientid, azureTenantIDAnnotation, a.config.TenantID)
		if serviceAccount.Annotations == nil {
			serviceAccount.Annotations = make(map[string]string)
		}
		serviceAccount.Annotations[azureClientidAnnotation] = *clientid
		serviceAccount.Annotations[azureTenantIDAnnotation] = a.config.TenantID
	} else {
		a.Logger.Info("Failed to find clientid for service account. No changes will be patched.", "name", a.serviceAccountQueryParameter.serviceAccountName, "namespace", a.serviceAccountQueryParameter.serviceAccountNamespace)
	}

	return &serviceAccount, nil
}

func (a azureQueryProvider) searchForClientIdInSubscriptions() (*string, error) {
	identities, err := a.getUamis(a.cred)
	if err != nil {
		return nil, err
	}

	ch := make(chan *string, 1)
	var clientFactories = map[string]*armmsi.ClientFactory{}

	for _, subscription := range a.Subscriptions.Value {
		shortenedSubscriptionId := strings.Split(subscription.ID, "/")[2]
		clientFactory, err := armmsi.NewClientFactory(shortenedSubscriptionId, a.cred, nil)
		if err != nil {
			a.Logger.Error(err, "failed to create federated identity query client")
		}
		clientFactories[shortenedSubscriptionId] = clientFactory
	}

	a.Logger.Info("Detected identities to check", "identitiesCount", len(identities))

	wg := sync.WaitGroup{}
	wg.Add(len(identities))

	for _, identity := range identities {
		go func(ch chan *string, identity *armmsi.Identity) {
			defer wg.Done()
			a.Logger.Info("Checking identity", "clientId", *identity.Properties.ClientID)
			resourceGroup := strings.Split(*identity.ID, "/")[4]
			resourceName := strings.Split(*identity.ID, "/")[8]

			federatedIdentityCredentials, err := a.getFederatedIdentityCredentialsForUami(resourceGroup, resourceName, clientFactories[strings.Split(*identity.ID, "/")[2]])
			if err != nil || federatedIdentityCredentials == nil {
				return
			}
			for _, i := range *federatedIdentityCredentials {
				if *i.Properties.Issuer == a.config.OidcIssuerUrl && *i.Properties.Subject == "system:serviceaccount:"+a.serviceAccountQueryParameter.serviceAccountNamespace+":"+a.serviceAccountQueryParameter.serviceAccountName {
					a.Logger.Info("Found matching federated identity", "clientId", *identity.Properties.ClientID)
					ch <- identity.Properties.ClientID
				}
			}
			a.Logger.Info("Done checking identity: ", "clientId", *identity.Properties.ClientID)
		}(ch, identity)
	}

	go func() {
		wg.Wait()
		ch <- nil
		close(ch)
	}()

	v, ok := <-ch

	if !ok || v == nil {
		return nil, errors.New("failed to find clientid for service account")
	}

	return v, nil
}

// uses the resourceGroup and resourceName to return a pointer to a slice of FederatedIdentityCredentials
func (a *azureQueryProvider) getFederatedIdentityCredentialsForUami(resourceGroup string, resourceName string, clientFactory *armmsi.ClientFactory) (*[]*armmsi.FederatedIdentityCredential, error) {
	federatedIdentityCredentials := []*armmsi.FederatedIdentityCredential{}

	ctx := context.Background()
	a.Logger.Info("Getting federated identity credentials for uami", "resourceGroup", resourceGroup, "resourceName", resourceName)

	pager := clientFactory.NewFederatedIdentityCredentialsClient().NewListPager(resourceGroup, resourceName, nil)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		switch {
		case len(page.Value) == 0:
			a.Logger.Info("No federated identity credentials found for uami", "resourceGroup", resourceGroup, "resourceName", resourceName)
			return nil, nil
		case err != nil && len(federatedIdentityCredentials) == 0:
			a.Logger.Error(err, "failed to advance page and currently have no federated identity credentials")
			return nil, err
		case err != nil:
			a.Logger.Error(err, "failed to advance page but have some federated identity credentials")
			return &federatedIdentityCredentials, nil
		}
		federatedIdentityCredentials = append(federatedIdentityCredentials, page.Value...)
	}

	return &federatedIdentityCredentials, nil
}

type Subscription struct {
	ID   string `json:"id"`
	Name string `json:"displayName"`
}

type SubscriptionList struct {
	Value []Subscription `json:"value"`
}

func retrieveCurrentSubscriptionList() (*SubscriptionList, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, err
	}

	token, err := cred.GetToken(context.Background(), policy.TokenRequestOptions{Scopes: []string{"https://management.azure.com/.default"}})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("GET", "https://management.azure.com/subscriptions?api-version=2020-01-01", nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token.Token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var subs SubscriptionList
	if err := json.Unmarshal(body, &subs); err != nil {
		return nil, err
	}

	return &subs, nil
}

func (a *azureQueryProvider) getUamis(cred *azidentity.DefaultAzureCredential) ([]*armmsi.Identity, error) {
	argClient, err := arg.NewClient(azcore.TokenCredential(cred), nil)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()

	var subscriptionIdList []*string

	for _, sub := range a.Subscriptions.Value {
		subscriptionIdList = append(subscriptionIdList, to.Ptr(strings.Split(sub.ID, "/")[2]))
	}

	query := "resources | where type == \"microsoft.managedidentity/userassignedidentities\""

	if a.config.FilterTags != nil {
		for tagKey, tagValue := range a.config.FilterTags {
			if tagValue == "<SERVICE_ACCOUNT_NAME>" {
				tagValue = a.serviceAccountQueryParameter.serviceAccountName
			} else if tagValue == "<NAMESPACE>" {
				tagValue = a.serviceAccountQueryParameter.serviceAccountNamespace
			}
			if a.config.ClusterIdentifier != "" {
				tagKey = a.config.ClusterIdentifier + "-" + tagKey
			}
			query += fmt.Sprintf(" | where tags['%s'] == '%s'", tagKey, tagValue)
		}
	}

	a.Logger.Info("Querying for identities", "query", query)

	var skipToken *string = nil
	var initQuery bool = true
	var identities []*armmsi.Identity

	for skipToken != nil || initQuery {
		initQuery = false
		res, err := argClient.Resources(ctx, arg.QueryRequest{
			Query:         to.Ptr(query),
			Subscriptions: subscriptionIdList,
			Options: &arg.QueryRequestOptions{
				SkipToken: skipToken,
			},
		}, nil)

		if err != nil {
			panic(err)
		}

		if skipToken != nil {
			log.Printf("SkipToken:" + *res.SkipToken)
		}

		skipToken = res.SkipToken

		json_result, err := json.Marshal(res.Data)
		if err != nil {
			panic(err)
		}

		if err := json.Unmarshal(json_result, &identities); err != nil {
			log.Fatalf("Failed to unmarshal result: %v", err)
		}
	}

	return identities, nil
}
