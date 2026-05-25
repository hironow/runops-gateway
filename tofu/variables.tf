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

# -----------------------------------------------------------------------------
# Token broker (refs#0007, ADR 0032) — Cloud Run env wiring.
# -----------------------------------------------------------------------------
#
# These drive the BROKER_* / GITHUB_APP_* env vars consumed by
# internal/composition/broker_config.go and gated by cmd/server/main.go
# (broker mounts only when BROKER_AUDIENCE is non-empty). All default to
# "" so the broker stays DORMANT: an apply with these unset injects empty
# env values, which the code treats as "broker disabled" — no behaviour
# change for the existing Slack / admin endpoints.
#
# Declaring them here (rather than setting them out-of-band via
# `gcloud run services update --update-env-vars`) is deliberate: the Cloud
# Run service's lifecycle.ignore_changes covers only `image`, so any env
# set outside tofu would be reverted on the next `tofu apply`. Keeping the
# broker env under tofu makes activation durable.
#
# Values are auth_boundary (grant matrix per ADR 0032). The canonical
# source is GitHub repo variables piped to TF_VAR_* by the cd.yaml infra
# Apply step; they are governed by repo-settings edit permission, not by
# PR review of committed tfvars.

variable "broker_audience" {
  description = "BROKER_AUDIENCE: broker URL pinned in the aud claim of every caller identity token. Empty (default) keeps the broker dormant (POST /broker/token not mounted)."
  type        = string
  default     = ""

  validation {
    condition     = var.broker_audience == "" || can(regex("^https://", var.broker_audience))
    error_message = "broker_audience must be an https:// URL, or empty to keep the broker dormant."
  }
}

variable "broker_gateway_service_sas" {
  description = "BROKER_GATEWAY_SERVICE_SAS: comma-separated SA emails allowed to mint via the cloudrun_iam verifier (gateway-service caller). Empty until broker activation."
  type        = string
  default     = ""
}

variable "broker_workspace_daemon_sas" {
  description = "BROKER_WORKSPACE_DAEMON_SAS: comma-separated SA emails allowed to mint via the workload_identity verifier (workspace-daemon caller). Empty until broker activation."
  type        = string
  default     = ""
}

variable "broker_operator_emails" {
  description = "BROKER_OPERATOR_EMAILS: comma-separated human operator emails allowed to mint via the gcloud_identity verifier. Empty until broker activation."
  type        = string
  default     = ""
}

variable "github_app_id" {
  description = "GITHUB_APP_ID: numeric GitHub App ID whose installation tokens the broker mints. Empty until broker activation."
  type        = string
  default     = ""

  validation {
    condition     = var.github_app_id == "" || can(regex("^[0-9]+$", var.github_app_id))
    error_message = "github_app_id must be a positive integer (the numeric App ID, not the slug), or empty."
  }
}

variable "github_app_private_key_secret_name" {
  description = "GITHUB_APP_PRIVATE_KEY_SECRET_NAME: Secret Manager resource name (projects/<p>/secrets/<s>/versions/<v>) the broker fetches the GitHub App private key from in production. The key VALUE is uploaded out-of-band (tofu state never sees it). Empty until broker activation."
  type        = string
  default     = ""

  validation {
    condition     = var.github_app_private_key_secret_name == "" || can(regex("^projects/.+/secrets/.+/versions/.+$", var.github_app_private_key_secret_name))
    error_message = "github_app_private_key_secret_name must be a Secret Manager resource name 'projects/<p>/secrets/<s>/versions/<v>', or empty."
  }
}

variable "broker_use_firestore_registry" {
  description = "BROKER_USE_FIRESTORE_REGISTRY: 'true'/'1' selects the Firestore-backed agent session registry (Cloud Run multi-instance safe); empty/'false'/'0' keeps the in-memory registry. Requires GOOGLE_CLOUD_PROJECT when true."
  type        = string
  default     = ""

  validation {
    condition     = contains(["", "true", "false", "1", "0"], var.broker_use_firestore_registry)
    error_message = "broker_use_firestore_registry must be one of \"\", \"true\", \"false\", \"1\", \"0\"."
  }
}
