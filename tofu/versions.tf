terraform {
  required_version = ">= 1.8.0"

  # Remote state on GCS.
  # Bucket and prefix are injected at init time via -backend-config so that
  # the bucket name stays outside version control.
  # CI/CD requires GitHub variable: TOFU_STATE_BUCKET
  # Local usage: tofu init -backend-config="bucket=YOUR_BUCKET"
  backend "gcs" {
    prefix = "runops-gateway/state"
  }

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 6.0"
    }
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
}
