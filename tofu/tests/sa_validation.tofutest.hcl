# Test harness for the exe_coder_vm_sa_email validation block (added
# in commit c7ce7c4 to address codex pre-push review #4 — cross-repo
# SA hand-off as a single point of failure).
#
# These run blocks deliberately exercise the validation rule itself
# rather than re-checking the variable's static contract from the
# pytest side; the existence of the block as text is uninteresting,
# what matters is that 'tofu plan' actually rejects a misconfigured
# SA before any real apply runs in CI.
#
# command = plan keeps the test free of side effects: no provider
# auth required, no resources created. mock_provider stubs the
# google provider so the rest of the configuration evaluates.
#
# Test matrix:
#   1. workspace SA pattern (exe-workspace@…)         -> plan succeeds
#   2. control-plane SA misuse (exe-coder@…)          -> plan fails on var.exe_coder_vm_sa_email
#   3. arbitrary other SA (chatops-deployer@…)         -> plan fails on var.exe_coder_vm_sa_email
#   4. empty (bootstrap before SA exists)              -> plan succeeds (allowed by validation)

mock_provider "google" {}

# google_service_account.* resources auto-generate computed `name` /
# `email` strings via mock_provider, but the google provider's own
# IAM bindings re-validate those names against a strict regex (must
# look like 'projects/<p>/serviceAccounts/<email>'). The auto-fill
# is shorter than the regex requires, so plan fails before the
# validation we want to test ever runs. Pinning realistic values
# here unblocks plan without changing what's being tested — the SA
# values themselves are placeholders.
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
  # Minimum required variables — values are placeholders, only the
  # variable under test (exe_coder_vm_sa_email) actually matters.
  project_id          = "test-project"
  region              = "asia-northeast1"
  image               = "asia-northeast1-docker.pkg.dev/test-project/runops/runops-gateway:test"
  allowed_slack_users = ""
  github_repo         = "hironow/runops-gateway"
  tofu_state_bucket   = "test-state-bucket"
}

run "workspace_sa_email_is_accepted" {
  command = plan

  variables {
    exe_coder_vm_sa_email = "exe-workspace@test-project.iam.gserviceaccount.com"
  }

  # No expect_failures and no assert: a plan that completes is the
  # success signal. If the validation block were to mistakenly reject
  # the workspace pattern, plan would fail and the test would fail
  # with a diagnostic from the validation block.
}

run "empty_email_is_accepted_during_bootstrap" {
  command = plan

  variables {
    exe_coder_vm_sa_email = ""
  }
}

run "control_plane_sa_email_is_rejected" {
  command = plan

  variables {
    exe_coder_vm_sa_email = "exe-coder@test-project.iam.gserviceaccount.com"
  }

  expect_failures = [var.exe_coder_vm_sa_email]
}

run "unrelated_sa_email_is_rejected" {
  command = plan

  variables {
    exe_coder_vm_sa_email = "chatops-deployer@test-project.iam.gserviceaccount.com"
  }

  expect_failures = [var.exe_coder_vm_sa_email]
}
