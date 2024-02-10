package provider

import (
	"errors"

	"github.com/go-logr/logr"
	"github.com/shiftavenue/azure-clientid-syncer/pkg/config"
	corev1 "k8s.io/api/core/v1"
)

type queryProvider interface {
	Query() (*corev1.ServiceAccount, error)
}

func NewQueryProvider(serviceAccount *corev1.ServiceAccount, logger logr.Logger, config config.Config) (queryProvider, error) {
	switch config.ProviderType {
	case "azure":
		return NewAzureQueryProvider(serviceAccount, logger, config)
	case "gcp":
		return NewGCPQueryProvider(serviceAccount, logger, config)
	default:
		return nil, errors.New("unknown provider type: " + config.ProviderType)
	}
}

type defaultQueryProvider struct {
	Logger logr.Logger
	config config.Config
	serviceAccount *corev1.ServiceAccount
}
