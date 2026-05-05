# Pub/Sub IAM for the D-Mail bridge consumers (Phase 4b).
#
# Two consumers:
#
#   1. runops-gateway (Cloud Run) -> chatops_sa
#      - publish dmail-inbound  (dispatch)
#      - publish dmail-inbound  (Phase 4a approval ack, ADR 0019)
#      - subscribe dmail-outbound-gateway (Phase 3 OutboundReceiver, ADR 0018)
#
#   2. exe-coder VM (systemd, hironow/dotfiles repo) -> exe_coder_vm_sa_email
#      - subscribe dmail-inbound-receiver
#      - publish   dmail-outbound (dmail-emitter)
#
# The VM SA bindings are conditional on var.exe_coder_vm_sa_email being set so
# this file applies cleanly during initial bootstrap before that SA exists.

# --- runops-gateway (chatops_sa) ---

resource "google_pubsub_topic_iam_member" "chatops_inbound_publisher" {
  project = var.project_id
  topic   = google_pubsub_topic.dmail_inbound.name
  role    = "roles/pubsub.publisher"
  member  = "serviceAccount:${google_service_account.chatops_sa.email}"
}

resource "google_pubsub_subscription_iam_member" "chatops_outbound_subscriber" {
  project      = var.project_id
  subscription = google_pubsub_subscription.dmail_outbound_gateway.name
  role         = "roles/pubsub.subscriber"
  member       = "serviceAccount:${google_service_account.chatops_sa.email}"
}

# --- exe-coder VM (var.exe_coder_vm_sa_email) ---

resource "google_pubsub_subscription_iam_member" "exe_coder_inbound_subscriber" {
  count = var.exe_coder_vm_sa_email == "" ? 0 : 1

  project      = var.project_id
  subscription = google_pubsub_subscription.dmail_inbound_receiver.name
  role         = "roles/pubsub.subscriber"
  member       = "serviceAccount:${var.exe_coder_vm_sa_email}"
}

resource "google_pubsub_topic_iam_member" "exe_coder_outbound_publisher" {
  count = var.exe_coder_vm_sa_email == "" ? 0 : 1

  project = var.project_id
  topic   = google_pubsub_topic.dmail_outbound.name
  role    = "roles/pubsub.publisher"
  member  = "serviceAccount:${var.exe_coder_vm_sa_email}"
}
