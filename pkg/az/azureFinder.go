package az

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
)

type FederatedIdentityCredentialQueryParams struct {
	Namespace          string
	ServiceAccountName string
}

type AzureFinder struct {
	OidcIssuerUrl  string
	Subscriptions  SubscriptionList
	Logger         logr.Logger
	cred           *azidentity.DefaultAzureCredential
	queryParameter FederatedIdentityCredentialQueryParams
	config         config.Config
}

func NewAzureFinder(oidcIssuerUrl string, logger logr.Logger, queryParameter FederatedIdentityCredentialQueryParams, config config.Config) (*AzureFinder, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		logger.Error(err, "failed to obtain a credential")
		return nil, err
	}
	return &AzureFinder{
		OidcIssuerUrl:  oidcIssuerUrl,
		Logger:         logger,
		cred:           cred,
		queryParameter: queryParameter,
		config:         config,
	}, nil
}

func (azureFinder *AzureFinder) GetclientidForServiceAccount() (string, error) {
	clientId, err := azureFinder.searchForClientIdInSubscriptions()

	if err != nil {
		return "", err
	} else if clientId == nil {
		return "", errors.New("failed to find client id in subscriptions")
	}

	return *clientId, nil
}

func (azureFinder AzureFinder) searchForClientIdInSubscriptions() (*string, error) {
	identities, err := azureFinder.getUamis(azureFinder.cred)
	if err != nil {
		return nil, err
	}

	ch := make(chan *string, 1)
	ctx, cancel := context.WithCancel(context.Background())

	var clientFactories = map[string]*armmsi.ClientFactory{}

	for _, subscription := range azureFinder.Subscriptions.Value {
		shortenedSubscriptionId := strings.Split(subscription.ID, "/")[2]
		clientFactory, err := armmsi.NewClientFactory(shortenedSubscriptionId, azureFinder.cred, nil)
		if err != nil {
			azureFinder.Logger.Error(err, "failed to create federated identity query client")
		}
		clientFactories[shortenedSubscriptionId] = clientFactory
	}

	azureFinder.Logger.Info("Detected identities to check", "identitiesCount", len(identities))

	wg := sync.WaitGroup{}
	wg.Add(len(identities))

	for _, identity := range identities {
		go func(ch chan *string, identity *armmsi.Identity, ctx context.Context) {

			azureFinder.Logger.Info("Checking identity", "clientId", *identity.Properties.ClientID)
			resourceGroup := strings.Split(*identity.ID, "/")[4]
			resourceName := strings.Split(*identity.ID, "/")[8]

			federatedIdentityCredentials, err := azureFinder.getFederatedIdentityCredentialsForUami(resourceGroup, resourceName, clientFactories[strings.Split(*identity.ID, "/")[2]], ctx)
			if err != nil || federatedIdentityCredentials == nil {
				return
			}
			for _, i := range *federatedIdentityCredentials {
				if *i.Properties.Issuer == azureFinder.OidcIssuerUrl && *i.Properties.Subject == "system:serviceaccount:"+azureFinder.queryParameter.Namespace+":"+azureFinder.queryParameter.ServiceAccountName {
					azureFinder.Logger.Info("Found matching federated identity", "clientId", *identity.Properties.ClientID)
					ch <- identity.Properties.ClientID
				}
			}
			azureFinder.Logger.Info("Done checking identity: ", "clientId", *identity.Properties.ClientID)
			wg.Done()
		}(ch, identity, ctx)
	}

	go func(ctx context.Context) {
		wg.Wait()
		if ctx.Err() != nil {
			return
		}
		ch <- nil
	}(ctx)

	v, ok := <-ch
	cancel()
	close(ch)

	if !ok || v == nil {
		return nil, nil
	}

	return v, nil
}

// uses the resourceGroup and resourceName to return a pointer to a slice of FederatedIdentityCredentials
func (azureFinder *AzureFinder) getFederatedIdentityCredentialsForUami(resourceGroup string, resourceName string, clientFactory *armmsi.ClientFactory, ctxOuter context.Context) (*[]*armmsi.FederatedIdentityCredential, error) {
	federatedIdentityCredentials := []*armmsi.FederatedIdentityCredential{}

	ctx := context.Background()
	azureFinder.Logger.Info("Getting federated identity credentials for uami", "resourceGroup", resourceGroup, "resourceName", resourceName)

	pager := clientFactory.NewFederatedIdentityCredentialsClient().NewListPager(resourceGroup, resourceName, nil)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		switch {
		case len(page.Value) == 0:
			azureFinder.Logger.Info("No federated identity credentials found for uami", "resourceGroup", resourceGroup, "resourceName", resourceName)
			return nil, nil
		case err != nil && len(federatedIdentityCredentials) == 0:
			azureFinder.Logger.Error(err, "failed to advance page and currently have no federated identity credentials")
			return nil, err
		case err != nil:
			azureFinder.Logger.Error(err, "failed to advance page but have some federated identity credentials")
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

func (azureFinder *AzureFinder) updateSubscriptionList() error {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		azureFinder.Logger.Error(err, "failed to obtain a credential")
		return err
	}

	token, err := cred.GetToken(context.Background(), policy.TokenRequestOptions{Scopes: []string{"https://management.azure.com/.default"}})
	if err != nil {
		azureFinder.Logger.Error(err, "failed to get token")
		return err
	}

	req, err := http.NewRequest("GET", "https://management.azure.com/subscriptions?api-version=2020-01-01", nil)
	if err != nil {
		azureFinder.Logger.Error(err, "failed to create request")
		return err
	}

	req.Header.Set("Authorization", "Bearer "+token.Token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		azureFinder.Logger.Error(err, "failed to send request")
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		azureFinder.Logger.Error(err, "failed to read response body")
		return err
	}

	var subs SubscriptionList
	if err := json.Unmarshal(body, &subs); err != nil {
		azureFinder.Logger.Error(err, "failed to unmarshal response body")
		return err
	}

	azureFinder.Logger.Info("found subscriptions", "subscriptions", subs)
	azureFinder.Subscriptions = subs
	return nil
}

func (azureFinder *AzureFinder) getUamis(cred *azidentity.DefaultAzureCredential) ([]*armmsi.Identity, error) {
	err := azureFinder.updateSubscriptionList()
	if err != nil {
		azureFinder.Logger.Error(err, "failed to update subscription list")
		return nil, err
	}

	argClient, err := arg.NewClient(azcore.TokenCredential(cred), nil)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()

	var subscriptionIdList []*string

	for _, sub := range azureFinder.Subscriptions.Value {
		subscriptionIdList = append(subscriptionIdList, to.Ptr(strings.Split(sub.ID, "/")[2]))
	}

	query := "resources | where type == \"microsoft.managedidentity/userassignedidentities\""

	if azureFinder.config.FilterTags != nil {
		for tagKey, tagValue := range azureFinder.config.FilterTags {
			if tagValue == "<SERVICE_ACCOUNT_NAME>" {
				tagValue = azureFinder.queryParameter.ServiceAccountName
			} else if tagValue == "<NAMESPACE>" {
				tagValue = azureFinder.queryParameter.Namespace
			}
			if azureFinder.config.ClusterIdentifier != "" {
				tagKey = azureFinder.config.ClusterIdentifier + "-" + tagKey
			}
			query += fmt.Sprintf(" | where tags['%s'] == '%s'", tagKey, tagValue)
		}
	}

	azureFinder.Logger.Info("Querying for identities", "query", query)

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
