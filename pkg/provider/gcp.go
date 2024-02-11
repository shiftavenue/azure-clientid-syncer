package provider

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"sync"

	admin "cloud.google.com/go/iam/admin/apiv1"
	"cloud.google.com/go/iam/admin/apiv1/adminpb"
	"cloud.google.com/go/iam/apiv1/iampb"
	"github.com/go-logr/logr"
	"github.com/shiftavenue/azure-clientid-syncer/pkg/config"
	"google.golang.org/api/iterator"
	corev1 "k8s.io/api/core/v1"
)

type gcpQueryProvider struct {
	defaultQueryProvider
}

func NewGCPQueryProvider(serviceAccount *corev1.ServiceAccount, logger logr.Logger, config config.Config) (*gcpQueryProvider, error) {
	return &gcpQueryProvider{
		defaultQueryProvider: defaultQueryProvider{
			Logger:         logger,
			config:         config,
			serviceAccount: serviceAccount,
		},
	}, nil
}

func (g *gcpQueryProvider) Query() (*corev1.ServiceAccount, error) {
	// Create a new IAM client
	ctx := context.Background()
	iamClient, err := admin.NewIamClient(ctx)
	if err != nil {
		return nil, err
	}

	// List all service accounts
	req := &adminpb.ListServiceAccountsRequest{
		Name: fmt.Sprintf("projects/%s", g.config.GcpProjectId),
	}
	it := iamClient.ListServiceAccounts(ctx, req)

	ch := make(chan *string, 1)
	wg := sync.WaitGroup{}

	for {
		sa, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		wg.Add(1)

		go func(ch chan *string, sa *adminpb.ServiceAccount) {
			defer wg.Done()
			// Get IAM policy for the service account
			policyReq := &iampb.GetIamPolicyRequest{
				Resource: sa.Name,
			}
			policy, err := iamClient.GetIamPolicy(ctx, policyReq)
			if err != nil {
				log.Printf("failed to retrieve IAM policy: %v", err)
				return
			}

			// Iterate over policy bindings
			for _, member := range policy.Members(gcpRoleName) {
				fmt.Printf("Found member with role '%s': %s\n", gcpRoleName, member)
				namespace, serviceAccountName := extractSubstrings(member)
				if g.serviceAccount.Name == serviceAccountName && g.serviceAccount.Namespace == namespace {
					fmt.Printf("Project ID: %s, Namespace: %s, Service Account: %s\n", g.config.GcpProjectId, namespace, serviceAccountName)
					ch <- &sa.Name
				}
			}
		}(ch, sa)
	}

	go func() {
		wg.Wait()
		ch <- nil
		close(ch)
	}()

	v, ok := <-ch
	if !ok || v == nil {
		return nil, fmt.Errorf("failed to retrieve service account")
	}

	if g.serviceAccount.Annotations == nil {
		g.serviceAccount.Annotations = make(map[string]string)
	}
	g.serviceAccount.Annotations[gcpServiceAccountAnnotation] = *v

	return g.serviceAccount, nil
}

func extractSubstrings(member string) (string, string) {
	// Use a regular expression to extract the namespace and service account
	re := regexp.MustCompile(`\[(.*?)\/(.*?)\]`)
	match := re.FindStringSubmatch(member)

	namespace := match[1]
	serviceAccount := match[2]

	return namespace, serviceAccount
}
