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

# DLQ terminal sinks: a pull subscription on each DLQ topic so forwarded
# messages survive past topic retention (14d) and can be inspected by a
# human operator. Per the GCP 'Handle message failures' doc, a DLQ topic
# with no subscriptions silently drops every message — see
# experiments/2026-05-05_pubsub-dlq-terminal-sink.md and docs/runbooks/dlq.md
# for the operator-facing triage flow.
#
# Differences from the working subscriptions above:
#   - enable_message_ordering = OFF (per-target order is meaningless after
#     5 deliveries failed on each individual message)
#   - no dead_letter_policy (DLQ-of-DLQ would be turtles all the way down)

resource "google_pubsub_subscription" "dmail_inbound_dlq_pull" {
  name  = "dmail-inbound-dlq-pull"
  topic = google_pubsub_topic.dmail_inbound_dlq.id

  ack_deadline_seconds       = 60
  message_retention_duration = "1209600s" # 14d, matches topic
  retain_acked_messages      = false
  enable_message_ordering    = false

  expiration_policy {
    ttl = "" # never expire
  }

  labels = {
    component = "dmail-bridge"
    role      = "dlq-terminal-sink"
  }
}

resource "google_pubsub_subscription" "dmail_outbound_dlq_pull" {
  name  = "dmail-outbound-dlq-pull"
  topic = google_pubsub_topic.dmail_outbound_dlq.id

  ack_deadline_seconds       = 60
  message_retention_duration = "1209600s"
  retain_acked_messages      = false
  enable_message_ordering    = false

  expiration_policy {
    ttl = ""
  }

  labels = {
    component = "dmail-bridge"
    role      = "dlq-terminal-sink"
  }
}
