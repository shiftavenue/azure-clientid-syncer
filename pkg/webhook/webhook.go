package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"github.com/shiftavenue/azure-clientid-syncer/pkg/config"
	"github.com/shiftavenue/azure-clientid-syncer/pkg/kuberneteshelper"
	"github.com/shiftavenue/azure-clientid-syncer/pkg/provider"
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
		client:  client,
		reader:  reader,
		config:  c,
		logger:  log,
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

	config, err := config.ParseConfig()
	if err != nil {
		m.logger.Error(err, "failed to parse config")
		return admission.Errored(http.StatusInternalServerError, err)
	}

	if config.AutoDetectOidcIssuerUrl {
		kuberneteshelper, err := kuberneteshelper.NewKubernetesHelper(m.logger)
		if err != nil {
			m.logger.Error(err, "failed to create KubernetesHelper")
			return admission.Errored(http.StatusInternalServerError, err)
		}
		config.OidcIssuerUrl, err = kuberneteshelper.GetOidcIssuerUrl()
		if err != nil {
			m.logger.Error(err, "failed to get OIDC issuer URL")
			return admission.Errored(http.StatusInternalServerError, err)
		}
		m.logger.Info("detected OIDC issuer URL: " + config.OidcIssuerUrl)
	}

	providerType := "azure"
	queryProvider, err := provider.NewQueryProvider(providerType, m.logger, *config)
	if err != nil {
		m.logger.Error(err, "failed to create query provider")
		return admission.Errored(http.StatusInternalServerError, err)
	}
	
	annotatedServiceAccount, err := queryProvider.Query(*serviceAccount)
	if err != nil {
		m.logger.Error(err, "failed to query service account")
		return admission.Errored(http.StatusInternalServerError, err)
	}

	marshaledServiceAccount, err := json.Marshal(annotatedServiceAccount)
	if err != nil {
		m.logger.Error(err, "failed to marshal service account")
		return admission.Errored(http.StatusInternalServerError, err)
	}
	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledServiceAccount)
}
