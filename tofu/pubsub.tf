# Pub/Sub topology for the D-Mail bridge (ADR 0013 / 0015 / 0017 / 0018).
#
# Topics:
#   - dmail-inbound      : runops-gateway -> exe-coder dmail-receiver
#   - dmail-inbound-dlq  : DLQ for dmail-inbound
#   - dmail-outbound     : exe-coder dmail-emitter -> runops-gateway
#   - dmail-outbound-dlq : DLQ for dmail-outbound
#
# Subscriptions and IAM live in their own files (subscriptions.tf, iam_pubsub.tf
# in follow-up commits) so each Phase 4b chore commit stays focused.

resource "google_pubsub_topic" "dmail_inbound" {
  name = "dmail-inbound"

  message_retention_duration = "604800s" # 7 days; safe headroom for VM preempt

  labels = {
    component = "dmail-bridge"
    direction = "inbound"
    adr       = "0013"
  }
}

resource "google_pubsub_topic" "dmail_inbound_dlq" {
  name = "dmail-inbound-dlq"

  # DLQ keeps messages longer so we can investigate after-the-fact.
  message_retention_duration = "1209600s" # 14 days

  labels = {
    component = "dmail-bridge"
    direction = "inbound"
    role      = "dlq"
  }
}

resource "google_pubsub_topic" "dmail_outbound" {
  name = "dmail-outbound"

  message_retention_duration = "604800s" # 7 days

  labels = {
    component = "dmail-bridge"
    direction = "outbound"
    adr       = "0018"
  }
}

resource "google_pubsub_topic" "dmail_outbound_dlq" {
  name = "dmail-outbound-dlq"

  message_retention_duration = "1209600s" # 14 days

  labels = {
    component = "dmail-bridge"
    direction = "outbound"
    role      = "dlq"
  }
}
