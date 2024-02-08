package provider

import (
	"errors"

	"github.com/go-logr/logr"
	"github.com/shiftavenue/azure-clientid-syncer/pkg/config"
	corev1 "k8s.io/api/core/v1"
)

type queryProvider interface {
	Query(corev1.ServiceAccount) (*corev1.ServiceAccount, error)
}

func NewQueryProvider(providerType string, logger logr.Logger, config config.Config) (queryProvider, error) {
	switch providerType {
	case "azure":
		return NewAzureQueryProvider(logger, config)
	default:
		return nil, errors.New("unknown provider type: " + providerType)
	}
}

type defaultQueryProvider struct {
	Logger logr.Logger
	config config.Config
}

type serviceAccountQueryParameter struct {
	serviceAccountName      string
	serviceAccountNamespace string
}
