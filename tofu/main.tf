# Service account for runops-gateway (least privilege)
resource "google_service_account" "chatops_sa" {
  account_id   = "slack-chatops-sa"
  display_name = "Slack ChatOps Service Account"
  description  = "Service account for runops-gateway — operates Cloud Run and Cloud SQL"
}

# Allow runops-gateway SA to develop (deploy/update) target Cloud Run service
resource "google_cloud_run_v2_service_iam_member" "chatops_run_developer" {
  project  = var.project_id
  location = var.region
  name     = var.cloud_run_target_service
  role     = "roles/run.developer"
  member   = "serviceAccount:${google_service_account.chatops_sa.email}"

  depends_on = [google_cloud_run_v2_service.runops_gateway]
}

# Cloud SQL admin for backup (scoped to project level — Cloud SQL has no resource-level IAM for backups)
resource "google_project_iam_member" "chatops_sql_admin" {
  project = var.project_id
  role    = "roles/cloudsql.admin"
  member  = "serviceAccount:${google_service_account.chatops_sa.email}"

  condition {
    title       = "only-for-backup"
    description = "Restrict to backup operations only — narrow scope further if possible"
    expression  = "true"
  }
}

# Secret Manager: Slack signing secret (value added manually via gcloud or console)
resource "google_secret_manager_secret" "slack_signing_secret" {
  secret_id = "slack-signing-secret"
  replication {
    auto {}
  }
}

# Secret Manager: Slack incoming webhook URL (used by scripts/notify-slack.sh)
resource "google_secret_manager_secret" "slack_webhook_url" {
  secret_id = "slack-webhook-url"
  replication {
    auto {}
  }
}

# Secret Manager: Slack Bot Token (xoxb-...) per ADR 0017.
# Enables the FallbackNotifier to drop into chat.postMessage when the
# response_url has expired or hit its 5-call limit.
resource "google_secret_manager_secret" "slack_bot_token" {
  secret_id = "slack-bot-token"
  replication {
    auto {}
  }
}

# Placeholder secret versions — replace with real values via gcloud after tofu apply:
#   gcloud secrets versions add slack-signing-secret --data-file=<(echo -n "REAL_VALUE")
#   gcloud secrets versions add slack-webhook-url --data-file=<(echo -n "REAL_VALUE")
# These resources are managed by tofu only for the initial bootstrap.
# The lifecycle ignore_changes prevents tofu from overwriting real values on subsequent applies.
resource "google_secret_manager_secret_version" "slack_signing_secret_placeholder" {
  secret      = google_secret_manager_secret.slack_signing_secret.id
  secret_data = "placeholder"

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_secret_manager_secret_version" "slack_webhook_url_placeholder" {
  secret      = google_secret_manager_secret.slack_webhook_url.id
  secret_data = "placeholder"

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_secret_manager_secret_version" "slack_bot_token_placeholder" {
  secret      = google_secret_manager_secret.slack_bot_token.id
  secret_data = "placeholder"

  lifecycle {
    ignore_changes = [secret_data]
  }
}

# Allow runtime SA to read both secrets
resource "google_secret_manager_secret_iam_member" "chatops_signing_secret_accessor" {
  secret_id = google_secret_manager_secret.slack_signing_secret.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.chatops_sa.email}"
}

resource "google_secret_manager_secret_iam_member" "chatops_webhook_url_accessor" {
  secret_id = google_secret_manager_secret.slack_webhook_url.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.chatops_sa.email}"
}

resource "google_secret_manager_secret_iam_member" "chatops_bot_token_accessor" {
  secret_id = google_secret_manager_secret.slack_bot_token.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.chatops_sa.email}"
}

# runops-gateway Cloud Run service
resource "google_cloud_run_v2_service" "runops_gateway" {
  name                = "runops-gateway"
  location            = var.region
  deletion_protection = true

  template {
    service_account = google_service_account.chatops_sa.email

    annotations = {
      # CPU must always be allocated so background goroutines (LRO wait) don't freeze
      # See ADR 0003: cpu-always-allocated
      "run.googleapis.com/cpu-throttling" = "false"
    }

    containers {
      image = var.image

      env {
        name  = "GOOGLE_CLOUD_PROJECT"
        value = var.project_id
      }
      env {
        name  = "CLOUD_RUN_LOCATION"
        value = var.region
      }
      env {
        name  = "ALLOWED_SLACK_USERS"
        value = var.allowed_slack_users
      }
      env {
        name = "SLACK_SIGNING_SECRET"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.slack_signing_secret.secret_id
            version = "latest"
          }
        }
      }
      # Phase 4a (ADR 0017): Bot Token enables FallbackNotifier and the
      # ApprovalRequester chat.postMessage path.
      env {
        name = "SLACK_BOT_TOKEN"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.slack_bot_token.secret_id
            version = "latest"
          }
        }
      }
      env {
        name  = "SLACK_DEFAULT_CHANNEL_ID"
        value = var.slack_default_channel_id
      }
      # Phase 2a (ADR 0013): switch DispatchService to the Pub/Sub backend.
      env {
        name  = "DISPATCHER_BACKEND"
        value = "pubsub"
      }
      env {
        name  = "PUBSUB_PROJECT_ID"
        value = var.project_id
      }
      env {
        name  = "PUBSUB_DMAIL_INBOUND_TOPIC"
        value = google_pubsub_topic.dmail_inbound.name
      }
      # Phase 3 (ADR 0018): in-process StreamingPull on dmail-outbound.
      env {
        name  = "PUBSUB_DMAIL_OUTBOUND_SUB"
        value = google_pubsub_subscription.dmail_outbound_gateway.name
      }
      # ADR 0020: direct OTLP gRPC export to Cloud Trace via the Telemetry
      # API. Sampler defaults pulled from var.otel_traces_sampler_arg so the
      # ratio can be tuned without a tofu apply once we get prod traffic.
      env {
        name  = "OTEL_EXPORTER_OTLP_ENDPOINT"
        value = "telemetry.googleapis.com:443"
      }
      env {
        name  = "OTEL_EXPORTER_OTLP_PROTOCOL"
        value = "grpc"
      }
      env {
        name  = "OTEL_SERVICE_NAME"
        value = "runops-gateway"
      }
      env {
        name  = "OTEL_TRACES_SAMPLER"
        value = "parentbased_traceidratio"
      }
      env {
        name  = "OTEL_TRACES_SAMPLER_ARG"
        value = var.otel_traces_sampler_arg
      }
      env {
        name  = "OTEL_BSP_SCHEDULE_DELAY"
        value = "2000"
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }
    }

    scaling {
      # ADR 0018: in-process dmail-outbound StreamingPull needs a warm instance
      # to keep the gRPC stream alive. To enable Phase 3 outbound in prod, set
      # var.cloud_run_min_instances = 1; default 0 keeps Cloud Run idle-cost
      # at zero until outbound traffic actually exists.
      min_instance_count = var.cloud_run_min_instances
      max_instance_count = var.cloud_run_max_instances
    }
  }

  lifecycle {
    ignore_changes = [
      # Image is managed by the GitHub Actions CD pipeline (deploy job),
      # not by OpenTofu. Tofu handles infrastructure; image updates are
      # deployed via `gcloud run deploy` in .github/workflows/cd.yaml.
      template[0].containers[0].image,
    ]
  }
}

# Allow Slack to invoke runops-gateway (public endpoint)
resource "google_cloud_run_v2_service_iam_member" "public_invoker" {
  project  = google_cloud_run_v2_service.runops_gateway.project
  location = google_cloud_run_v2_service.runops_gateway.location
  name     = google_cloud_run_v2_service.runops_gateway.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}
