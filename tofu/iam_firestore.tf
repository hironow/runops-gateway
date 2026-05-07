# Firestore IAM for the gateway's multiplex project registry (Issue #0011).
#
# `roles/datastore.user` covers Add / Get / List / Update on the registry's
# documents — the four operations defined by port.ProjectRegistry. It does
# NOT include create/delete of the database itself or schema-changing
# admin scopes; those stay with the human operator running tofu.
#
# Note: Firestore IAM is granted at the project level (not per-database),
# so this binding gives the SA access to every Firestore database in the
# project. The named-database isolation in firestore.tf still serves its
# primary goal — preventing google_firestore_database creation conflicts —
# but operators should be aware that segregating registry data from other
# Firestore use cases requires either a dedicated GCP project or per-DB
# IAM (currently in preview, intentionally avoided for Phase α).

resource "google_project_iam_member" "chatops_firestore_user" {
  project = var.project_id
  role    = "roles/datastore.user"
  member  = "serviceAccount:${google_service_account.chatops_sa.email}"
}
