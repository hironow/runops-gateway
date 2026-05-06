# Artifact Registry repository for runops-gateway container images.
# Images are pushed here by the GitHub Actions CD pipeline.
resource "google_artifact_registry_repository" "runops" {
  location      = var.region
  repository_id = "runops"
  format        = "DOCKER"
  description   = "Container images for runops-gateway"

  cleanup_policies {
    id     = "delete-untagged"
    action = "DELETE"
    condition {
      tag_state  = "UNTAGGED"
      older_than = "86400s" # 1 day
    }
  }
}

# Workspace VM SA reader grant on the runops AR repo (ADR 0023).
#
# The dmail-receiver / dmail-emitter daemons run as 'docker run'
# units under host-OS systemd on each workspace VM (managed in
# hironow/dotfiles). The workspace VM's attached service account
# (var.exe_coder_vm_sa_email — the variable name is preserved from
# ADR 0015 era for backwards compatibility, but in the ADR 0023
# topology the *value* is set to the workspace-VM SA, not the
# control-plane VM SA) needs roles/artifactregistry.reader scoped
# to this single repo so 'docker pull' against the dmail image
# tags works at workspace boot.
#
# Conditional on var.exe_coder_vm_sa_email being set so this file
# applies cleanly during initial bootstrap (matches the same
# pattern used in iam_pubsub.tf).
resource "google_artifact_registry_repository_iam_member" "exe_coder_runops_reader" {
  count = var.exe_coder_vm_sa_email == "" ? 0 : 1

  project    = google_artifact_registry_repository.runops.project
  location   = google_artifact_registry_repository.runops.location
  repository = google_artifact_registry_repository.runops.name
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${var.exe_coder_vm_sa_email}"
}
