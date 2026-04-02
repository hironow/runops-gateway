output "runops_gateway_url" {
  description = "Public URL of the runops-gateway Cloud Run service — set this as the Slack App Request URL"
  value       = google_cloud_run_v2_service.runops_gateway.uri
}

output "chatops_sa_email" {
  description = "Runtime service account email for runops-gateway"
  value       = google_service_account.chatops_sa.email
}

output "github_deployer_sa_email" {
  description = "Service account email for GitHub Actions — set as GCP_SERVICE_ACCOUNT repository variable"
  value       = google_service_account.github_deployer.email
}

output "workload_identity_provider" {
  description = "Workload Identity Provider resource name — set as GCP_WORKLOAD_IDENTITY_PROVIDER repository variable"
  value       = google_iam_workload_identity_pool_provider.github.name
}

output "artifact_registry_repository" {
  description = "Artifact Registry repository URI (without image name)"
  value       = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.runops.repository_id}"
}

output "slack_signing_secret_name" {
  description = "Secret Manager secret ID for the Slack signing secret — add a version with the actual value"
  value       = google_secret_manager_secret.slack_signing_secret.secret_id
}

output "slack_webhook_url_secret_name" {
  description = "Secret Manager secret ID for the Slack webhook URL — add a version with the actual value"
  value       = google_secret_manager_secret.slack_webhook_url.secret_id
}
