package kuberneteshelper

import (
	"context"
	"encoding/json"

	"github.com/go-logr/logr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type KubernetesHelper struct {
	logger    logr.Logger
	clientSet *kubernetes.Clientset
}

type oidcConfig struct {
	Issuer string `json:"issuer"`
}

func NewKubernetesHelper(log logr.Logger) (*KubernetesHelper, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Info("Failed to get in cluster config, trying kubeconfig")
		config, err = clientcmd.BuildConfigFromFlags("", "kubeconfig")
		if err != nil {
			log.Error(err, "Failed to get kubeconfig")
			return nil, err
		}
	}

	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Error(err, "Failed to create clientset")
		return nil, err
	}
	return &KubernetesHelper{
		logger:    log,
		clientSet: clientSet,
	}, nil
}

func (k *KubernetesHelper) GetOidcIssuerUrl() (string, error) {
	rawData, err := k.clientSet.RESTClient().Get().AbsPath("/.well-known/openid-configuration").DoRaw(context.Background())
	if err != nil {
		k.logger.Error(err, "Failed to get oidc config")
		return "", err
	}

	var config oidcConfig
	err = json.Unmarshal(rawData, &config)
	if err != nil {
		k.logger.Error(err, "Failed to unmarshal oidc config")
		return "", err
	}

	return config.Issuer, nil
}
