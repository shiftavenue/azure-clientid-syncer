package az

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/go-logr/logr"
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
}

func NewAzureFinder(oidcIssuerUrl string, logger logr.Logger, queryParameter FederatedIdentityCredentialQueryParams) (*AzureFinder, error) {
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
	}, nil
}

func (azureFinder *AzureFinder) GetclientidForServiceAccount() (string, error) {
	err := azureFinder.updateSubscriptionList()
	if err != nil {
		azureFinder.Logger.Error(err, "failed to update subscription list")
		return "", err
	}

	ch := make(chan string, 1)
	wg := sync.WaitGroup{}
	wg.Add(len(azureFinder.Subscriptions.Value))

	for _, sub := range azureFinder.Subscriptions.Value {
		go azureFinder.searchForClientIdInSubscription(strings.Split(sub.ID, "/")[2], ch, &wg)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	v, ok := <-ch

	if !ok {
		return "", errors.New("failed to find clientId")
	}

	return v, nil
}

func (azureFinder AzureFinder) searchForClientIdInSubscription(subscriptionId string, ch chan string, externalWG *sync.WaitGroup) {
	clientFactory, err := armmsi.NewClientFactory(subscriptionId, azureFinder.cred, nil)
	if err != nil {
		azureFinder.Logger.Error(err, "failed to create client")
	}

	identities := azureFinder.getUamis(clientFactory)

	wg := sync.WaitGroup{}
	wg.Add(len(identities))

	for _, identity := range identities {
		go func(ch chan string, identity *armmsi.Identity) {
			azureFinder.Logger.Info("Checking identity", "clientId", *identity.Properties.ClientID)
			resourceGroup := strings.Split(*identity.ID, "/")[4]
			resourceName := strings.Split(*identity.ID, "/")[8]
			for _, i := range azureFinder.getFederatedIdentityCredentialsForUami(resourceGroup, resourceName, clientFactory) {
				if *i.Properties.Issuer == azureFinder.OidcIssuerUrl && *i.Properties.Subject == "system:serviceaccount:"+azureFinder.queryParameter.Namespace+":"+azureFinder.queryParameter.ServiceAccountName {
					ch <- *identity.Properties.ClientID
				}
			}
			azureFinder.Logger.Info("Done checking identity: ", "clientId", *identity.Properties.ClientID)
			wg.Done()
		}(ch, identity)
	}

	go func() {
		wg.Wait()
		externalWG.Done()
	}()
}

// uses the resourceGroup and resourceName to return a pointer to a slice of FederatedIdentityCredentials
func (azureFinder *AzureFinder) getFederatedIdentityCredentialsForUami(resourceGroup string, resourceName string, clientFactory *armmsi.ClientFactory) []*armmsi.FederatedIdentityCredential {
	federatedIdentityCredentials := []*armmsi.FederatedIdentityCredential{}

	ctx := context.Background()
	pager := clientFactory.NewFederatedIdentityCredentialsClient().NewListPager(resourceGroup, resourceName, &armmsi.FederatedIdentityCredentialsClientListOptions{Top: nil,
		Skiptoken: nil,
	})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			azureFinder.Logger.Error(err, "failed to advance page")
		}
		federatedIdentityCredentials = append(federatedIdentityCredentials, page.Value...)
	}
	return federatedIdentityCredentials
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

func (azureFinder *AzureFinder) getUamis(clientFactory *armmsi.ClientFactory) []*armmsi.Identity {
	UserAssignedIdentitiesListResult := []*armmsi.Identity{}

	ctx := context.Background()
	pager := clientFactory.NewUserAssignedIdentitiesClient().NewListBySubscriptionPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			azureFinder.Logger.Error(err, "failed to advance page")
		}
		UserAssignedIdentitiesListResult = append(UserAssignedIdentitiesListResult, page.Value...)
	}

	return UserAssignedIdentitiesListResult
}
