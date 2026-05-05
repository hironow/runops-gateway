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

variable "slack_default_channel_id" {
  description = <<-EOT
    Slack channel ID used by FallbackNotifier when the original
    response_url has expired and no in-thread channel context is
    available (ADR 0017). Empty disables the fallback (primary errors
    propagate as before — Phase 0 behaviour).
  EOT
  type        = string
  default     = ""
}

variable "otel_traces_sampler_arg" {
  description = <<-EOT
    OTEL_TRACES_SAMPLER_ARG passed to runops-gateway. Used with
    'parentbased_traceidratio'. Start at 0.1 and tune as Pub/Sub-driven
    span volume reveals itself in Cloud Trace quota usage.
  EOT
  type        = string
  default     = "0.1"
}

variable "dlq_alert_email" {
  description = <<-EOT
    Email address that receives Cloud Monitoring incidents when the D-Mail
    Pub/Sub bridge forwards a message to a DLQ. Empty disables the alert
    + notification channel (handy for early bootstrap before email routing
    is decided). See docs/runbooks/dlq.md for the triage workflow.
  EOT
  type        = string
  default     = ""
}

variable "exe_coder_vm_sa_email" {
  description = <<-EOT
    Service account email of the exe-coder VM (managed in hironow/dotfiles).
    Granted pubsub.subscriber on dmail-inbound-receiver and pubsub.publisher on
    dmail-outbound so the dmail-receiver and dmail-emitter daemons can run
    against the production topology. Leave empty until the exe-coder VM SA is
    actually provisioned — IAM bindings are created only when this is set.
  EOT
  type        = string
  default     = ""
}
