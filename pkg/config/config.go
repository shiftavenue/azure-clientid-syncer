package config

import (
	"errors"

	"github.com/kelseyhightower/envconfig"
)

// Config holds configuration from the env variables
type Config struct {
	TenantID                string `envconfig:"AZURE_TENANT_ID"`
	AutoDetectOidcIssuerUrl bool   `envconfig:"AUTO_DETECT_OIDC_ISSUER_URL"`
	OidcIssuerUrl           string `envconfig:"OIDC_ISSUER_URL"`
	// add filter tags here via 'export FILTER_TAGS="aks-clientid-syncer:true"'.
	// There are also two special tags: <NAMESPACE> and <SERVICE_ACCOUNT_NAME> which will be replaced with the actual values of the mutation request during runtime.
	FilterTags map[string]string `envconfig:"FILTER_TAGS"`
	// acts as a prefix for the tags in the azure portal allowing multi tenancy
	ClusterIdentifier string `envconfig:"CLUSTER_IDENTIFIER"`
}

// ParseConfig parses the configuration from env variables
func ParseConfig() (*Config, error) {
	c := new(Config)
	if err := envconfig.Process("config", c); err != nil {
		return c, err
	}
	if c.OidcIssuerUrl == "" && !c.AutoDetectOidcIssuerUrl {
		return nil, errors.New("OIDC_ISSUER_URL or AUTO_DETECT_OIDC_ISSUER_URL must be set")
	}
	if c.TenantID == "" {
		return nil, errors.New("AZURE_TENANT_ID must be set")
	}

	return c, nil
}
