# Cloud Monitoring alert for the D-Mail bridge (Phase 4b backfill).
#
# Triggers on any DLQ forwarding event in the last 5 minutes. The threshold
# is 0 because at the project's traffic level (tens of msgs/day) every DLQ
# delivery is, by definition, an anomaly worth waking up for.
#
# See:
#   - docs/runbooks/dlq.md
#   - experiments/2026-05-05_pubsub-dlq-terminal-sink.md
#
# The notification channel + alert are conditional on var.dlq_alert_email so
# initial bootstrap works without an alert destination yet.

resource "google_monitoring_notification_channel" "dlq_email" {
  count = var.dlq_alert_email == "" ? 0 : 1

  display_name = "runops-gateway DLQ"
  type         = "email"
  labels = {
    email_address = var.dlq_alert_email
  }
}

resource "google_monitoring_alert_policy" "dmail_dlq_forwarding" {
  count = var.dlq_alert_email == "" ? 0 : 1

  display_name = "D-Mail DLQ message forwarded"
  combiner     = "OR"

  conditions {
    display_name = "Any message forwarded to a DLQ in last 5 min"

    condition_threshold {
      filter = join(" AND ", [
        "resource.type = \"pubsub_subscription\"",
        "metric.type = \"pubsub.googleapis.com/subscription/dead_letter_message_count\"",
        "(resource.label.subscription_id = \"${google_pubsub_subscription.dmail_inbound_receiver.name}\" OR resource.label.subscription_id = \"${google_pubsub_subscription.dmail_outbound_gateway.name}\")",
      ])
      comparison      = "COMPARISON_GT"
      threshold_value = 0
      duration        = "0s"

      aggregations {
        alignment_period   = "300s"
        per_series_aligner = "ALIGN_DELTA" # delta over 5 min, not gauge
      }
    }
  }

  notification_channels = [google_monitoring_notification_channel.dlq_email[0].id]

  alert_strategy {
    auto_close = "1800s" # 30m silent → auto-close
  }

  documentation {
    content   = "See docs/runbooks/dlq.md in the runops-gateway repo for the triage steps."
    mime_type = "text/markdown"
  }
}
