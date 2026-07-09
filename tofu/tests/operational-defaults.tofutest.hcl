# Test harness for default value assertions on operational variables.
#
# These variables have no validation block; Layer 1 coverage is the
# default-value contract itself — wrong defaults are silent breakage that
# would only surface on the next tofu apply. Running this as a plan-time
# assertion catches drift before it reaches production.
#
# Covered defaults:
#   region, cloud_run_target_service, cloud_sql_instance,
#   slack_default_channel_id, cloud_run_min_instances, cloud_run_max_instances,
#   otel_traces_sampler_arg, dlq_alert_email, firestore_database_name,
#   firestore_location_id, exe_coder_vm_sa_email,
#   github_token, github_repo_owner, github_repo_name, release_gate_app_id_numeric
#
# command = plan keeps the test side-effect free.
# mock_provider stubs google and github so no auth is required.

mock_provider "google" {}
mock_provider "github" {}

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
  project_id          = "test-project"
  image               = "asia-northeast1-docker.pkg.dev/test-project/runops/runops-gateway:test"
  allowed_slack_users = ""
  github_repo         = "hironow/runops-gateway"
  tofu_state_bucket   = "test-state-bucket"
}

run "operational_defaults_are_correct" {
  command = plan

  assert {
    condition     = var.region == "asia-northeast1"
    error_message = "region default must be asia-northeast1."
  }

  assert {
    condition     = var.cloud_run_target_service == "runops-gateway"
    error_message = "cloud_run_target_service default must be runops-gateway."
  }

  assert {
    condition     = var.cloud_sql_instance == ""
    error_message = "cloud_sql_instance default must be empty."
  }

  assert {
    condition     = var.slack_default_channel_id == ""
    error_message = "slack_default_channel_id default must be empty."
  }

  assert {
    condition     = var.cloud_run_min_instances == 0
    error_message = "cloud_run_min_instances default must be 0 (cold-start / cost-minimal)."
  }

  assert {
    condition     = var.cloud_run_max_instances == 3
    error_message = "cloud_run_max_instances default must be 3."
  }

  assert {
    condition     = var.otel_traces_sampler_arg == "0.1"
    error_message = "otel_traces_sampler_arg default must be \"0.1\"."
  }

  assert {
    condition     = var.dlq_alert_email == ""
    error_message = "dlq_alert_email default must be empty."
  }

  assert {
    condition     = var.firestore_database_name == "runops-registry"
    error_message = "firestore_database_name default must be runops-registry."
  }

  assert {
    condition     = var.firestore_location_id == "asia-northeast1"
    error_message = "firestore_location_id default must be asia-northeast1."
  }

  assert {
    condition     = var.exe_coder_vm_sa_email == ""
    error_message = "exe_coder_vm_sa_email default must be empty (bootstrap state)."
  }

  assert {
    condition     = var.github_token == ""
    error_message = "github_token default must be empty (plan-only contexts)."
  }

  assert {
    condition     = var.github_repo_owner == ""
    error_message = "github_repo_owner default must be empty."
  }

  assert {
    condition     = var.github_repo_name == ""
    error_message = "github_repo_name default must be empty (disables rulesets via count=0)."
  }

  assert {
    condition     = var.release_gate_app_id_numeric == 0
    error_message = "release_gate_app_id_numeric default must be 0 (no bypass actor)."
  }
}
