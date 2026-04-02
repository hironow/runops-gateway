# Artifact Registry repository for runops-gateway container images.
# Images are pushed here by the GitHub Actions CD pipeline.
resource "google_artifact_registry_repository" "runops" {
  location      = var.region
  repository_id = "runops"
  format        = "DOCKER"
  description   = "Container images for runops-gateway"
}
