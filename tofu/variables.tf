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

variable "cloud_run_min_instances" {
  description = <<-EOT
    runops-gateway Cloud Run service の min_instance_count。
    ADR 0018 (dmail-outbound StreamingPull) は warm instance を要求するため
    Phase 3 outbound 機能を本番有効化するときは 1 に上げる。それまでは 0
    (cold start 許容、コスト最小) が default。
  EOT
  type        = number
  default     = 0
}

variable "cloud_run_max_instances" {
  description = "runops-gateway Cloud Run service の max_instance_count"
  type        = number
  default     = 3
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

variable "firestore_database_name" {
  description = <<-EOT
    Named Firestore database used as the multiplex project registry SoT
    (Issue #0011). MUST NOT be "(default)" — see firestore.tf for the
    rationale. The runtime gateway reads RUNOPS_FIRESTORE_DATABASE to
    select this database via firestore.NewClientWithDatabase.
  EOT
  type        = string
  default     = "runops-registry"

  validation {
    condition     = var.firestore_database_name != "(default)" && length(var.firestore_database_name) > 0
    error_message = "firestore_database_name must be a non-empty named Firestore database, not \"(default)\". Use \"runops-registry\" or another project-unique identifier."
  }
}

variable "firestore_location_id" {
  description = <<-EOT
    Firestore native location ID for the multiplex project registry.
    Single-region IDs (e.g. asia-northeast1) keep latency aligned with
    the gateway Cloud Run region. Multi-region IDs (nam5/eur3) are
    permitted but require an ADR update because they change the cost
    model and disaster-recovery story.
  EOT
  type        = string
  default     = "asia-northeast1"

  validation {
    condition     = length(var.firestore_location_id) > 0
    error_message = "firestore_location_id must be non-empty."
  }
}

variable "exe_coder_vm_sa_email" {
  description = <<-EOT
    Service account email of the VM that runs dmail-receiver / dmail-emitter
    (managed in hironow/dotfiles). Variable name is preserved from the
    ADR 0015 era when the daemons were targeted at the exe-coder control-
    plane VM; per ADR 0023 the daemons now run on each workspace VM, so
    the *value* MUST be the workspace-VM SA (e.g. exe-workspace@…) not
    the control-plane VM SA. The pattern is enforced by the validation
    block below. Granted:

      - pubsub.subscriber  on dmail-inbound-receiver
      - pubsub.publisher   on dmail-outbound topic
      - cloudtrace.agent   project-level
      - artifactregistry.reader scoped to the runops AR repo (so
        'docker pull' of dmail-receiver / dmail-emitter image tags
        works at workspace boot)

    Leave empty until the workspace VM SA is actually provisioned — IAM
    bindings are created only when this is set.
  EOT
  type        = string
  default     = ""

  # Codex pre-push review #4 (2026-05-06) flagged this variable as a
  # cross-repo single point of failure: the legacy variable name
  # ('exe_coder_*') invites the operator to plug in the control-plane
  # SA, but per ADR 0023 the daemons now run on the workspace VM and
  # need the workspace SA. A misroute here causes the dmail daemons to
  # come up against production Pub/Sub with the wrong identity and fail
  # immediately on permissions — the fastest possible production failure
  # mode after deploy. We block this at the tofu validate / plan layer:
  # the empty default is allowed (initial bootstrap, before the SA exists),
  # and any non-empty value must look like a workspace-VM SA (i.e. start
  # with 'exe-workspace@' and end with '.iam.gserviceaccount.com'). Renaming
  # the variable itself is preferable but requires a co-ordinated GitHub
  # variable + dotfiles handoff that sits outside this commit.
  validation {
    condition = var.exe_coder_vm_sa_email == "" || (
      can(regex("^exe-workspace@[a-z0-9-]+\\.iam\\.gserviceaccount\\.com$",
      var.exe_coder_vm_sa_email))
    )
    error_message = "exe_coder_vm_sa_email must be the workspace VM SA (per ADR 0023): an email of the form 'exe-workspace@<project>.iam.gserviceaccount.com', or empty during bootstrap. Plugging in the control-plane SA (exe-coder@…) would let the daemons start with the wrong identity and fail at first Pub/Sub call. If you really need a different naming convention, rename the variable in tofu/variables.tf and update this validation."
  }
}
