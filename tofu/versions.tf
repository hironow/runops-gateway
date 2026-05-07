terraform {
  required_version = ">= 1.11.0"

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
      version = ">= 7.26.0"
    }
    # GitHub provider for Repository Ruleset / branch protection /
    # tag protection management (ADR 0031, ADR 0033). Apply requires
    # a GitHub App installation token, supplied via TF_VAR_github_token
    # by the cd.yaml dispatch infra-apply job. Empty token is allowed
    # for plan-only workflows; resource creation will fail without
    # auth, which is the intended fail-closed behaviour.
    github = {
      source  = "integrations/github"
      version = "~> 6.0"
    }
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
}

provider "github" {
  # Token comes from TF_VAR_github_token, sourced from
  # actions/create-github-app-token@<commit-SHA> in the cd.yaml
  # dispatch infra-apply job (ADR 0033 §"GitHub App for Repository
  # Ruleset management"). Empty in local dev or plan-only contexts.
  token = var.github_token
  owner = var.github_repo_owner
}
