package util

import "os"

func GetNamespace() string {
	ns, found := os.LookupEnv("SA_NAMESPACE")
	if !found {
		return "azure-clientid-syncer-system"
	}
	return ns
}
