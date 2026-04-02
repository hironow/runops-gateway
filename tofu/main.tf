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
  replication { auto {} }
}

# Secret Manager: Slack incoming webhook URL (used by scripts/notify-slack.sh)
resource "google_secret_manager_secret" "slack_webhook_url" {
  secret_id = "slack-webhook-url"
  replication { auto {} }
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

# runops-gateway Cloud Run service
resource "google_cloud_run_v2_service" "runops_gateway" {
  name     = "runops-gateway"
  location = var.region

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
    }

    scaling {
      min_instance_count = 0
      max_instance_count = 3
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
