package util

import "os"

func GetNamespace() string {
	ns, found := os.LookupEnv("POD_NAMESPACE")
	if !found {
		return "azure-clientid-syncer"
	}
	return ns
}
