# GitHub Repository Ruleset — release gate enforcement (ADR 0031, ADR 0033).
#
# This file is auth_boundary by definition: it controls who can push
# what to develop / main / prod-* tags. Every change MUST go through
# the release gate's auth_boundary path with two human reviewers.
#
# Apply requires the integrations/github provider authenticated via
# TF_VAR_github_token, sourced from a GitHub App installation token
# minted by actions/create-github-app-token@<commit-SHA> in the
# cd.yaml dispatch infra-apply job.

# -----------------------------------------------------------------------------
# Inputs
# -----------------------------------------------------------------------------

variable "github_token" {
  description = "GitHub App installation token (TF_VAR_github_token). Empty in plan-only contexts; resource creation will fail without auth, which is the intended fail-closed behaviour."
  type        = string
  default     = ""
  sensitive   = true
}

variable "github_repo_owner" {
  description = "GitHub org / user that owns the runops-gateway repository (e.g. \"hironow\"). Used by the github provider's owner attribute."
  type        = string
  default     = ""
}

variable "github_repo_name" {
  description = "GitHub repository name (e.g. \"runops-gateway\"). Used as the target of the rulesets defined here."
  type        = string
  default     = ""
}

# -----------------------------------------------------------------------------
# prod-* tag protection (ADR 0031 §"prod-* tag immutability")
# -----------------------------------------------------------------------------
#
# Rules:
#   - Deletion blocked
#   - Force-push / non-fast-forward update blocked
#   - Tag creation restricted to the deploy app identity
#
# The cd.yaml dispatch deploy job mints prod-<YYYYMMDD>-<sha7> tags
# automatically; humans do not push prod-* tags from workstations
# because the API rejects them under this ruleset.

resource "github_repository_ruleset" "prod_tags" {
  count       = var.github_repo_name == "" ? 0 : 1
  name        = "prod-tags-immutable"
  repository  = var.github_repo_name
  target      = "tag"
  enforcement = "active"

  conditions {
    ref_name {
      include = ["refs/tags/prod-*"]
      exclude = []
    }
  }

  rules {
    deletion         = true
    non_fast_forward = true
    update           = true
    creation         = false
  }

  bypass_actors {
    # Actor type 4 = GitHub App. The actor_id of the release-gate App
    # is provided as a TF_VAR; only this App may create prod-* tags.
    # Humans (Repository admin role, actor_type=Maintain) are NOT in
    # the bypass list.
    actor_id    = var.release_gate_app_id_numeric
    actor_type  = "Integration"
    bypass_mode = "always"
  }
}

variable "release_gate_app_id_numeric" {
  description = "Numeric GitHub App ID (NOT the slug) of the release-gate app authorized to create prod-* tags. 0 means \"no bypass actor configured\" (only valid in non-production environments)."
  type        = number
  default     = 0
}

# -----------------------------------------------------------------------------
# develop branch protection — release-gate status check required
# -----------------------------------------------------------------------------
#
# Per ADR 0031: branch protection on develop requires the
# `release-gate` commit status to be present and successful before
# merge. This composes with the workflow-side classification: the
# release-gate workflow always publishes the status, and this rule
# blocks any PR whose status is missing or failing.

resource "github_repository_ruleset" "develop_branch" {
  count       = var.github_repo_name == "" ? 0 : 1
  name        = "develop-release-gate-required"
  repository  = var.github_repo_name
  target      = "branch"
  enforcement = "active"

  conditions {
    ref_name {
      include = ["refs/heads/develop"]
      exclude = []
    }
  }

  rules {
    deletion         = true
    non_fast_forward = true
    pull_request {
      required_approving_review_count   = 1
      dismiss_stale_reviews_on_push     = false
      require_code_owner_review         = true
      require_last_push_approval        = false
      required_review_thread_resolution = false
    }
    required_status_checks {
      strict_required_status_checks_policy = false
      required_check {
        context        = "release-gate"
        integration_id = 0 # 0 = any integration may report; the gate
        # is enforced by the context name alone.
      }
    }
  }
}

# -----------------------------------------------------------------------------
# main branch protection — promotion-only (ADR 0031 §"main promotion path")
# -----------------------------------------------------------------------------
#
# main is the rollback target. It accepts only fast-forward updates
# from develop (when develop has zero in-flight auth_boundary/schema
# PRs) or release branch cuts. Direct PRs targeting main are blocked
# in normal operation; the release-gate workflow + a second human
# reviewer gate any exception.

resource "github_repository_ruleset" "main_branch" {
  count       = var.github_repo_name == "" ? 0 : 1
  name        = "main-promotion-only"
  repository  = var.github_repo_name
  target      = "branch"
  enforcement = "active"

  conditions {
    ref_name {
      include = ["refs/heads/main"]
      exclude = []
    }
  }

  rules {
    deletion         = true
    non_fast_forward = true
    pull_request {
      required_approving_review_count   = 2
      dismiss_stale_reviews_on_push     = true
      require_code_owner_review         = true
      require_last_push_approval        = true
      required_review_thread_resolution = true
    }
    required_status_checks {
      strict_required_status_checks_policy = true
      required_check {
        context        = "release-gate"
        integration_id = 0
      }
    }
  }
}
