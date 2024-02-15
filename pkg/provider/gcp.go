package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	asset "cloud.google.com/go/asset/apiv1"
	"cloud.google.com/go/asset/apiv1/assetpb"
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
	// Create a new Asset Inventory client
	ctx := context.Background()
	assetClient, err := asset.NewClient(ctx)
	if err != nil {
		return nil, err
	}

	// Find GCP service account that can be impersonated by Kubernetes account using the GCP Asset Inventory
	// the query looks for all resources on which the Kubernetes service account has the 'roles/iam.workloadIdentityUser' role assigned
	req := &assetpb.SearchAllIamPoliciesRequest{
		Scope: fmt.Sprintf("projects/%s", g.config.GcpProjectId),
		Query: fmt.Sprintf("policy:%s.svc.id.goog[%s/%s] roles:%s", g.config.GcpProjectId, g.serviceAccount.Namespace, g.serviceAccount.Name, gcpRoleName),
	}
	it := assetClient.SearchAllIamPolicies(ctx, req)

	// Iterate through all results and find service account
	gcpServiceAccountMail := ""
	for {
		res, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		// The service account resource is returned in the format: //iam.googleapis.com/projects/<project-id>/serviceAccounts/<sa-name>@<project-id>.iam.gserviceaccount.com
		// extract relevant account mail that can be used in the annotation
		if res.AssetType == gcpResourceAssetType {
			// Fail if two or more service accounts were found
			if gcpServiceAccountMail != "" {
				return nil, errors.New("multiple service accounts were found, cannot decide which one to use")
			} else {
				gcpServiceAccountMail = strings.Split(res.Resource, "/")[6]
				if g.serviceAccount.Annotations == nil {
					g.serviceAccount.Annotations = make(map[string]string)
				}
				g.serviceAccount.Annotations[gcpServiceAccountAnnotation] = gcpServiceAccountMail
			}
		}
	}

	return g.serviceAccount, nil
}
