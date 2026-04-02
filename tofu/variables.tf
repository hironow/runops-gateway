variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "region" {
  description = "GCP region for all resources"
  type        = string
  default     = "asia-northeast1"
}

variable "image" {
  description = <<-EOT
    Container image URI for the initial Cloud Run deployment.
    Example: asia-northeast1-docker.pkg.dev/PROJECT/runops/runops-gateway:latest
    Subsequent image updates are managed by the GitHub Actions CD pipeline
    (ignore_changes in main.tf prevents tofu from overwriting the running image).
  EOT
  type        = string
}

variable "allowed_slack_users" {
  description = "Comma-separated Slack user IDs allowed to approve operations (e.g. U0123ABCD,U0456EFGH)"
  type        = string
}

variable "github_repo" {
  description = <<-EOT
    GitHub repository in 'owner/repo' format.
    Used to restrict Workload Identity Federation to this repository only.
    Example: hironow/runops-gateway
  EOT
  type        = string
}

variable "tofu_state_bucket" {
  description = <<-EOT
    GCS bucket name for OpenTofu remote state.
    Must be created manually before running `tofu init` (bootstrapping constraint).
    Example: my-project-tofu-state
  EOT
  type        = string
}

variable "cloud_run_target_service" {
  description = "Default Cloud Run service name that runops-gateway is allowed to operate on"
  type        = string
  default     = "runops-gateway"
}

variable "cloud_sql_instance" {
  description = "Cloud SQL instance name for backup operations (optional)"
  type        = string
  default     = ""
}
