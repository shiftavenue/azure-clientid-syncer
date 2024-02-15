package provider

const (
	// clientidAnnotation represents the clientid to be used with pod
	azureClientidAnnotation = "azure.workload.identity/client-id"
	// TenantIDAnnotation represent the tenantID to be used with pod
	azureTenantIDAnnotation = "azure.workload.identity/tenant-id"

	// gcpRoleName represents the role associated with service accounts in GCP allowing them to use workload identity
	gcpRoleName = "roles/iam.workloadIdentityUser"
	// gcpResourceAssetType represents the Asset Inventory resource type for GCP service accounts
	gcpResourceAssetType = "iam.googleapis.com/ServiceAccount"
	// gcpServiceAccountAnnotation represents the GCP service account name to be used with the Kubernetes service account
	gcpServiceAccountAnnotation = "iam.gke.io/gcp-service-account"
)
