output "runops_gateway_url" {
  description = "Public URL of the runops-gateway Cloud Run service"
  value       = google_cloud_run_v2_service.runops_gateway.uri
}

output "chatops_sa_email" {
  description = "Service account email for runops-gateway"
  value       = google_service_account.chatops_sa.email
}
