package config

import (
	"log"

	"github.com/kelseyhightower/envconfig"
)

// Config holds configuration from the env variables
type Config struct {
	TenantID                string `envconfig:"AZURE_TENANT_ID"`
	AutoDetectOidcIssuerUrl bool   `envconfig:"AUTO_DETECT_OIDC_ISSUER_URL"`
	OidcIssuerUrl           string `envconfig:"OIDC_ISSUER_URL"`
}

// ParseConfig parses the configuration from env variables
func ParseConfig() (*Config, error) {
	c := new(Config)
	if err := envconfig.Process("config", c); err != nil {
		return c, err
	}
	if c.OidcIssuerUrl == "" && !c.AutoDetectOidcIssuerUrl {
		log.Fatal("OIDC_ISSUER_URL or AUTO_DETECT_OIDC_ISSUER_URL must be set")
	}

	return c, nil
}
