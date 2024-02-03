package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"github.com/shiftavenue/azure-clientid-syncer/pkg/az"
	"github.com/shiftavenue/azure-clientid-syncer/pkg/config"
	"github.com/shiftavenue/azure-clientid-syncer/pkg/kuberneteshelper"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhook:path=/mutate-v1-serviceaccount,mutating=true,failurePolicy=fail,groups="",resources=serviceaccounts,verbs=create,versions=v1,name=mutation.azure-clientid-syncer-webhook.io,sideEffects=None,admissionReviewVersions=v1;v1beta1,matchPolicy=Equivalent,reinvocationPolicy=IfNeeded
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;update

// this is required for the webhook server certs generated and rotated as part of cert-controller rotator
// +kubebuilder:rbac:groups="",namespace=azure-clientid-syncer-webhook-system,resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=mutatingwebhookconfigurations,verbs=get;list;watch;update

// serviceAccountMutator mutates serviceAccount objects to add clientid and tenantid annotations
type serviceAccountMutator struct {
	client client.Client
	// reader is an instance of mgr.GetAPIReader that is configured to use the API server.
	// This should be used sparingly and only when the client does not fit the use case.
	reader  client.Reader
	config  *config.Config
	decoder *admission.Decoder
	logger  logr.Logger
}

// NewServiceAccountMutator returns a service account mutation handler
func NewServiceAccountMutator(client client.Client, reader client.Reader, scheme *runtime.Scheme, log logr.Logger) (admission.Handler, error) {
	c, err := config.ParseConfig()
	if err != nil {
		return nil, err
	}

	if err := registerMetrics(); err != nil {
		return nil, errors.Wrap(err, "failed to register metrics")
	}

	return &serviceAccountMutator{
		client: client,
		reader: reader,
		config: c,
		logger: log,
		decoder: admission.NewDecoder(scheme),
	}, nil
}

// serviceAccountMutator adds annotations to service account objects if the service account can be linked to an Azure identity
func (m *serviceAccountMutator) Handle(ctx context.Context, req admission.Request) (response admission.Response) {
	timeStart := time.Now()
	m.logger.Info("received request to mutate service account")
	defer func() {
		ReportRequest(ctx, req.Namespace, time.Since(timeStart))
	}()

	serviceAccount := &corev1.ServiceAccount{}
	err := m.decoder.Decode(req, serviceAccount)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	serviceAccountName := serviceAccount.Name
	serviceAccountNamespace := serviceAccount.Namespace

	m.logger.Info("identified service account with name: " + serviceAccountName + " and namespace: " + serviceAccountNamespace)

	config, err := config.ParseConfig()
	if err != nil {
		panic(err)
	}

	if config.AutoDetectOidcIssuerUrl {
		kuberneteshelper, err := kuberneteshelper.NewKubernetesHelper(m.logger)
		if err != nil {
			panic(err)
		}
		config.OidcIssuerUrl, err = kuberneteshelper.GetOidcIssuerUrl()
		if err != nil {
			panic(err)
		}
		m.logger.Info("detected OIDC issuer URL: " + config.OidcIssuerUrl)
	}

	azFinder, err := az.NewAzureFinder(config.OidcIssuerUrl, m.logger, az.FederatedIdentityCredentialQueryParams{
		Namespace:          serviceAccountNamespace,
		ServiceAccountName: serviceAccountName,
	}, *config)
	if err != nil {
		m.logger.Error(err, "failed to create AzureFinder")
		return admission.Errored(http.StatusInternalServerError, err)
	}

	clientid, err := azFinder.GetclientidForServiceAccount()

	if err == nil {
		m.logger.Info("Setting new annotations for service account", "name", serviceAccountName, "namespace", serviceAccountNamespace, clientidAnnotation, clientid, TenantIDAnnotation, config.TenantID)
		if serviceAccount.Annotations == nil {
			serviceAccount.Annotations = make(map[string]string)
		}
		serviceAccount.Annotations[clientidAnnotation] = clientid
		serviceAccount.Annotations[TenantIDAnnotation] = config.TenantID
	} else {
		m.logger.Info("Failed to find clientid for service account. No changes will be patched.", "name", serviceAccountName, "namespace", serviceAccountNamespace,)
	}

	marshaledServiceAccount, err := json.Marshal(serviceAccount)
	if err != nil {
		m.logger.Error(err, "failed to marshal service account")
		return admission.Errored(http.StatusInternalServerError, err)
	}
	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledServiceAccount)
}

const (
	// clientidAnnotation represents the clientid to be used with pod
	clientidAnnotation = "azure.workload.identity/client-id"
	// TenantIDAnnotation represent the tenantID to be used with pod
	TenantIDAnnotation = "azure.workload.identity/tenant-id"
)
