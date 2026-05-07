# Firestore native database for the multiplex project registry (Issue #0011).
#
# We deliberately use a NAMED database (not the (default) DB) so that the
# resource always applies cleanly — a project may already have a (default)
# DB created out-of-band (Datastore mode, Firebase, App Engine, etc.) and
# `google_firestore_database` would then fail with AlreadyExists. Naming
# the DB sidesteps that conflict and keeps the registry isolated from
# anything else the project uses Firestore for.
#
# At runtime the gateway reads RUNOPS_FIRESTORE_DATABASE and routes to this
# named DB via firestore.NewClientWithDatabase. The emulator and CI use
# the (default) DB instead, and a missing/empty env var transparently
# falls back to (default) — see registry_factory.go and ADR 0026.

resource "google_firestore_database" "runops_registry" {
  project     = var.project_id
  name        = var.firestore_database_name
  location_id = var.firestore_location_id
  type        = "FIRESTORE_NATIVE"

  # Production registry is the SoT for project_id; deletion would be a
  # data loss event and must require explicit operator intent (run
  # `gcloud firestore databases update --delete-protection=DISABLED`
  # before `tofu destroy`). See ADR 0026.
  delete_protection_state = "DELETE_PROTECTION_ENABLED"
}
