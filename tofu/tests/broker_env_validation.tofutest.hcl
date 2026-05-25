# Test harness for the token broker (refs#0007, ADR 0032) Cloud Run env
# variables added to tofu/variables.tf + tofu/main.tf.
#
# Layer 1 (tofu test) owns: the variable validation blocks' real reject
# behaviour + the default values (dormancy contract). The pytest layer
# does NOT re-check these (per the repo IaC test policy).
#
# command = plan keeps the test side-effect free: mock_provider stubs
# google, no auth, no resources created.
#
# Test matrix:
#   1. defaults are all ""                                  -> dormant (assert)
#   2. valid activation values                              -> plan succeeds
#   3. broker_audience without https://                     -> rejected
#   4. github_app_id non-numeric                            -> rejected
#   5. github_app_private_key_secret_name wrong format      -> rejected
#   6. broker_use_firestore_registry invalid enum           -> rejected

mock_provider "google" {}

# Same auto-fill workaround as sa_validation: the google provider
# re-validates SA names referenced by IAM bindings against a strict
# regex; mock_provider's auto-generated value is too short and would
# fail plan before the broker validations under test ever run.
override_resource {
  target = google_service_account.github_deployer
  values = {
    name  = "projects/test-project/serviceAccounts/github-deployer@test-project.iam.gserviceaccount.com"
    email = "github-deployer@test-project.iam.gserviceaccount.com"
  }
}

override_resource {
  target = google_service_account.chatops_sa
  values = {
    name  = "projects/test-project/serviceAccounts/chatops@test-project.iam.gserviceaccount.com"
    email = "chatops@test-project.iam.gserviceaccount.com"
  }
}

variables {
  # Minimum required variables — placeholders; the broker_* variables
  # are what these run blocks exercise.
  project_id          = "test-project"
  region              = "asia-northeast1"
  image               = "asia-northeast1-docker.pkg.dev/test-project/runops/runops-gateway:test"
  allowed_slack_users = ""
  github_repo         = "hironow/runops-gateway"
  tofu_state_bucket   = "test-state-bucket"
}

run "broker_defaults_are_empty_and_dormant" {
  command = plan

  # No broker_* overrides: assert the dormancy contract holds via defaults.
  assert {
    condition     = var.broker_audience == ""
    error_message = "broker_audience default must be empty so the broker stays dormant."
  }
  assert {
    condition     = var.broker_gateway_service_sas == "" && var.broker_workspace_daemon_sas == "" && var.broker_operator_emails == ""
    error_message = "broker SA/operator allowlists must default to empty."
  }
  assert {
    condition     = var.github_app_id == "" && var.github_app_private_key_secret_name == ""
    error_message = "github_app_id and github_app_private_key_secret_name must default to empty."
  }
  assert {
    condition     = var.broker_use_firestore_registry == ""
    error_message = "broker_use_firestore_registry must default to empty (in-memory registry)."
  }
}

run "valid_activation_values_are_accepted" {
  command = plan

  variables {
    broker_audience                    = "https://broker.example.com"
    broker_gateway_service_sas         = "gw@test-project.iam.gserviceaccount.com"
    broker_workspace_daemon_sas        = "ws@test-project.iam.gserviceaccount.com"
    broker_operator_emails             = "op@example.com"
    github_app_id                      = "1234567"
    github_app_private_key_secret_name = "projects/test-project/secrets/github-app-private-key/versions/latest"
    broker_use_firestore_registry      = "true"
  }
}

run "broker_audience_without_https_is_rejected" {
  command = plan

  variables {
    broker_audience = "broker.example.com"
  }

  expect_failures = [var.broker_audience]
}

run "github_app_id_non_numeric_is_rejected" {
  command = plan

  variables {
    github_app_id = "app-slug"
  }

  expect_failures = [var.github_app_id]
}

run "private_key_secret_name_wrong_format_is_rejected" {
  command = plan

  variables {
    github_app_private_key_secret_name = "github-app-private-key"
  }

  expect_failures = [var.github_app_private_key_secret_name]
}

run "firestore_registry_invalid_enum_is_rejected" {
  command = plan

  variables {
    broker_use_firestore_registry = "yes"
  }

  expect_failures = [var.broker_use_firestore_registry]
}
