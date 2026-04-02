# ---------------------------------------------------------------------------
# Workload Identity Federation for GitHub Actions
#
# Allows the GitHub Actions CD pipeline to authenticate to GCP without
# long-lived service account keys (OIDC-based keyless auth).
#
# After applying, set these GitHub repository variables:
#   GCP_WORKLOAD_IDENTITY_PROVIDER = output.workload_identity_provider
#   GCP_SERVICE_ACCOUNT            = output.github_deployer_sa_email
# ---------------------------------------------------------------------------

# Workload Identity Pool — shared by all GitHub Actions workflows in this project.
resource "google_iam_workload_identity_pool" "github" {
  workload_identity_pool_id = "github"
  display_name              = "GitHub Actions"
  description               = "Identity pool for GitHub Actions OIDC authentication"
}

# Workload Identity Pool Provider — maps GitHub OIDC claims to GCP attributes.
resource "google_iam_workload_identity_pool_provider" "github" {
  workload_identity_pool_id          = google_iam_workload_identity_pool.github.workload_identity_pool_id
  workload_identity_pool_provider_id = "github"
  display_name                       = "GitHub Actions OIDC"

  attribute_mapping = {
    "google.subject"       = "assertion.sub"
    "attribute.actor"      = "assertion.actor"
    "attribute.repository" = "assertion.repository"
  }

  # Restrict to the specific repository — prevents other repos from impersonating.
  attribute_condition = "attribute.repository == '${var.github_repo}'"

  oidc {
    issuer_uri = "https://token.actions.githubusercontent.com"
  }
}

# Service account for the GitHub Actions deployer (separate from the runtime SA).
resource "google_service_account" "github_deployer" {
  account_id   = "chatops-github-deployer"
  display_name = "GitHub Actions Deployer"
  description  = "Impersonated by GitHub Actions CD via Workload Identity Federation"
}

# Allow the GitHub Actions workflow to impersonate the deployer SA.
resource "google_service_account_iam_member" "github_deployer_wif" {
  service_account_id = google_service_account.github_deployer.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.github.name}/attribute.repository/${var.github_repo}"
}

# --- Permissions for: docker push (Artifact Registry) ----------------------

resource "google_artifact_registry_repository_iam_member" "github_deployer_ar_writer" {
  project    = var.project_id
  location   = var.region
  repository = google_artifact_registry_repository.runops.repository_id
  role       = "roles/artifactregistry.writer"
  member     = "serviceAccount:${google_service_account.github_deployer.email}"
}

# --- Permissions for: gcloud run deploy (Cloud Run) -------------------------

resource "google_cloud_run_v2_service_iam_member" "github_deployer_run_developer" {
  project  = var.project_id
  location = var.region
  name     = google_cloud_run_v2_service.runops_gateway.name
  role     = "roles/run.developer"
  member   = "serviceAccount:${google_service_account.github_deployer.email}"
}

# --- Permissions for: tofu apply (infrastructure management) ----------------
#
# editor covers most resource creation/update.
# iam.securityAdmin is additionally needed for IAM binding management.
# secretmanager.admin is needed for Secret Manager resources.
#
# Consider using a narrower custom role in production if the threat model requires it.

resource "google_project_iam_member" "github_deployer_editor" {
  project = var.project_id
  role    = "roles/editor"
  member  = "serviceAccount:${google_service_account.github_deployer.email}"
}

resource "google_project_iam_member" "github_deployer_iam_admin" {
  project = var.project_id
  role    = "roles/iam.securityAdmin"
  member  = "serviceAccount:${google_service_account.github_deployer.email}"
}

resource "google_project_iam_member" "github_deployer_secret_admin" {
  project = var.project_id
  role    = "roles/secretmanager.admin"
  member  = "serviceAccount:${google_service_account.github_deployer.email}"
}

# --- Permissions for: tofu remote state (GCS) --------------------------------
#
# The state bucket is pre-created manually before `tofu init` (bootstrapping).
# tofu manages bucket IAM here, but not the bucket resource itself.

resource "google_storage_bucket_iam_member" "github_deployer_state_rw" {
  bucket = var.tofu_state_bucket
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.github_deployer.email}"
}
