# OpenTelemetry / Cloud Trace wiring (ADR 0020).
#
# Enables the Telemetry API (OTLP ingest endpoint at
# telemetry.googleapis.com:443) and grants tracesWriter to:
#   - chatops_sa (runops-gateway Cloud Run)
#   - exe_coder_vm_sa_email (dmail-receiver / dmail-emitter daemons)
#
# Cloud Trace itself ("cloudtrace.googleapis.com") is the legacy ingest path
# kept on for backwards compatibility; OTLP traffic flows through the new
# telemetry endpoint.

resource "google_project_service" "telemetry" {
  project = var.project_id
  service = "telemetry.googleapis.com"

  disable_on_destroy = false # keep enabled if this module is removed
}

resource "google_project_service" "cloudtrace" {
  project = var.project_id
  service = "cloudtrace.googleapis.com"

  disable_on_destroy = false
}

# runops-gateway (Cloud Run) -> tracesWriter on the project.
resource "google_project_iam_member" "chatops_traces_writer" {
  project = var.project_id
  role    = "roles/cloudtrace.agent"
  member  = "serviceAccount:${google_service_account.chatops_sa.email}"

  depends_on = [google_project_service.cloudtrace]
}

# exe-coder VM -> same. Conditional so initial bootstrap works before the SA
# is provisioned in hironow/dotfiles.
resource "google_project_iam_member" "exe_coder_traces_writer" {
  count = var.exe_coder_vm_sa_email == "" ? 0 : 1

  project = var.project_id
  role    = "roles/cloudtrace.agent"
  member  = "serviceAccount:${var.exe_coder_vm_sa_email}"

  depends_on = [google_project_service.cloudtrace]
}
