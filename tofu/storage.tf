# GCS bucket for OpenTofu remote state.
# This bucket is managed by OpenTofu itself (bootstrapped via local backend migration).
# See README section 1-1 for the bootstrap procedure.
resource "google_storage_bucket" "tofu_state" {
  name          = var.tofu_state_bucket
  location      = var.region
  project       = var.project_id
  force_destroy = false

  uniform_bucket_level_access = true

  versioning {
    enabled = true
  }

  lifecycle_rule {
    action {
      type = "Delete"
    }
    condition {
      num_newer_versions = 3
      with_state         = "ARCHIVED"
    }
  }
}
