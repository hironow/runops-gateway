variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "region" {
  description = "GCP region for Cloud Run deployment"
  type        = string
  default     = "asia-northeast1"
}

variable "image" {
  description = "Container image URI for runops-gateway (e.g. asia-northeast1-docker.pkg.dev/PROJECT/repo/runops-gateway:latest)"
  type        = string
}

variable "allowed_slack_users" {
  description = "Comma-separated list of Slack user IDs allowed to approve operations"
  type        = string
}

variable "cloud_run_target_service" {
  description = "Name of the Cloud Run service that runops-gateway will operate on"
  type        = string
  default     = "frontend-service"
}

variable "cloud_sql_instance" {
  description = "Cloud SQL instance name for backup operations"
  type        = string
  default     = ""
}
