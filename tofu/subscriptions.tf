# Pub/Sub subscriptions for the D-Mail bridge (ADR 0013 / 0018).
#
# Subscriptions:
#   - dmail-inbound-receiver   pull, exe-coder VM dmail-receiver consumes
#   - dmail-outbound-gateway   pull, runops-gateway internal subscriber consumes
#
# Both run StreamingPull from a long-lived process — dead_letter_policy with 5
# delivery attempts, then forward to the matching *-dlq topic for triage.
#
# IAM lives in iam_pubsub.tf (next commit) so this file stays purely about
# subscription shape.

resource "google_pubsub_subscription" "dmail_inbound_receiver" {
  name  = "dmail-inbound-receiver"
  topic = google_pubsub_topic.dmail_inbound.id

  ack_deadline_seconds = 60

  expiration_policy {
    ttl = "" # never expire (long-lived production subscription)
  }

  retry_policy {
    minimum_backoff = "10s"
    maximum_backoff = "600s"
  }

  dead_letter_policy {
    dead_letter_topic     = google_pubsub_topic.dmail_inbound_dlq.id
    max_delivery_attempts = 5
  }

  enable_message_ordering = true # ADR 0013: per-target ordering on target_tool

  labels = {
    component = "dmail-bridge"
    consumer  = "exe-coder-dmail-receiver"
  }
}

resource "google_pubsub_subscription" "dmail_outbound_gateway" {
  name  = "dmail-outbound-gateway"
  topic = google_pubsub_topic.dmail_outbound.id

  ack_deadline_seconds = 60

  expiration_policy {
    ttl = ""
  }

  retry_policy {
    minimum_backoff = "10s"
    maximum_backoff = "600s"
  }

  dead_letter_policy {
    dead_letter_topic     = google_pubsub_topic.dmail_outbound_dlq.id
    max_delivery_attempts = 5
  }

  # ordering not required on the result path — replies fan back independently
  enable_message_ordering = false

  labels = {
    component = "dmail-bridge"
    consumer  = "runops-gateway-internal"
  }
}

# Pub/Sub uses a Google-managed publisher SA to publish to the DLQ topic when
# delivery exceeds max attempts. Grant that SA the publisher role on each DLQ.
data "google_project" "current" {
  project_id = var.project_id
}

locals {
  pubsub_service_agent = "service-${data.google_project.current.number}@gcp-sa-pubsub.iam.gserviceaccount.com"
}

resource "google_pubsub_topic_iam_member" "inbound_dlq_publisher" {
  project = var.project_id
  topic   = google_pubsub_topic.dmail_inbound_dlq.name
  role    = "roles/pubsub.publisher"
  member  = "serviceAccount:${local.pubsub_service_agent}"
}

resource "google_pubsub_topic_iam_member" "outbound_dlq_publisher" {
  project = var.project_id
  topic   = google_pubsub_topic.dmail_outbound_dlq.name
  role    = "roles/pubsub.publisher"
  member  = "serviceAccount:${local.pubsub_service_agent}"
}

# DLQ-source subscriptions need to grant the same Pub/Sub service agent the
# subscriber role on the working subscription so it can ack messages on
# delivery-attempt exhaustion.
resource "google_pubsub_subscription_iam_member" "inbound_dlq_subscriber" {
  project      = var.project_id
  subscription = google_pubsub_subscription.dmail_inbound_receiver.name
  role         = "roles/pubsub.subscriber"
  member       = "serviceAccount:${local.pubsub_service_agent}"
}

resource "google_pubsub_subscription_iam_member" "outbound_dlq_subscriber" {
  project      = var.project_id
  subscription = google_pubsub_subscription.dmail_outbound_gateway.name
  role         = "roles/pubsub.subscriber"
  member       = "serviceAccount:${local.pubsub_service_agent}"
}
