# Token broker (refs#0007) — GitHub App private-key secret + IAM
# binding for the gateway Cloud Run service account.
#
# This file is the IaC counterpart to:
#   - internal/adapter/output/secret/secret_manager_private_key_fetcher.go
#     (Phase 2b-2-2b — production private-key sourcing)
#   - internal/composition/broker_dependencies.go
#     (Phase 3b-3b-1 — composition root that calls the fetcher)
#
# Workflow after `tofu apply`:
#
#   1. The secret resource exists with no payload version. The IaC
#      does NOT bake in the actual private key bytes — that happens
#      out-of-band:
#
#        gcloud secrets versions add github-app-private-key \
#          --data-file=/path/to/github-app.pem
#
#      keeping the PEM out of Terraform state.
#
#   2. The Cloud Run gateway SA (chatops_sa) has secretAccessor
#      access via google_secret_manager_secret_iam_member.
#
#   3. The cmd/server composition root reads the secret name from
#      the GITHUB_APP_PRIVATE_KEY_SECRET_NAME env var, builds a
#      *secret.SecretManagerPrivateKeyFetcher, and hands the bytes
#      to the ghinstallation minter at startup.
#
# ADR refs:
#   - ADR 0031 / 0033 (release-gate): this file lives under tofu/
#     so paths.yaml's `tofu/**/*.tf` glob already classifies edits
#     here as auth_boundary.
#   - ADR 0032 (broker grant matrix): this secret IS the GitHub App
#     identity — its rotation cadence drives the broker's
#     authentication boundary.

resource "google_secret_manager_secret" "github_app_private_key" {
  secret_id = "github-app-private-key"

  replication {
    auto {}
  }

  labels = {
    refs    = "0007"
    purpose = "token-broker-github-app"
  }
}

# Grant the Cloud Run gateway SA permission to read the secret value
# at startup. The composition root in cmd/server/main.go calls
# secretmanager.AccessSecretVersion once at process start; no other
# code path reads this secret.
resource "google_secret_manager_secret_iam_member" "chatops_github_app_accessor" {
  secret_id = google_secret_manager_secret.github_app_private_key.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.chatops_sa.email}"
}
