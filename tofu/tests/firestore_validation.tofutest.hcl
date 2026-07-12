# Test harness for the firestore_database_name + firestore_location_id
# validation blocks (Issue #0011, multiplex Phase α production cutover).
#
# command = plan keeps the test free of side effects: no provider auth
# required, no resources actually created. mock_provider stubs the google
# and github providers so the rest of the configuration evaluates.
#
# Test matrix:
#   1. firestore_database_name="runops-registry" -> plan succeeds
#   2. firestore_database_name="(default)"       -> plan fails (named DB required)
#   3. firestore_database_name=""                -> plan fails (non-empty required)
#   4. firestore_location_id=""                  -> plan fails (non-empty required)

mock_provider "google" {}
mock_provider "github" {}

# Same provider IAM regex workaround as sa_validation.tofutest.hcl: the
# google provider re-validates SA names against a strict regex that
# mock_provider's auto-fill does not satisfy.
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
  region              = "asia-northeast1"
  image               = "asia-northeast1-docker.pkg.dev/test-project/runops/runops-gateway:test"
  allowed_slack_users = ""
  github_repo         = "hironow/runops-gateway"
  tofu_state_bucket   = "test-state-bucket"
}

run "named_database_is_accepted" {
  command = plan

  variables {
    firestore_database_name = "runops-registry"
    firestore_location_id   = "asia-northeast1"
  }
}

run "default_database_is_rejected" {
  command = plan

  variables {
    firestore_database_name = "(default)"
    firestore_location_id   = "asia-northeast1"
  }

  expect_failures = [var.firestore_database_name]
}

run "empty_database_is_rejected" {
  command = plan

  variables {
    firestore_database_name = ""
    firestore_location_id   = "asia-northeast1"
  }

  expect_failures = [var.firestore_database_name]
}

run "empty_location_is_rejected" {
  command = plan

  variables {
    firestore_database_name = "runops-registry"
    firestore_location_id   = ""
  }

  expect_failures = [var.firestore_location_id]
}
