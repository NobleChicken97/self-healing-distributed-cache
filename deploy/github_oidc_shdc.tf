# GitHub OIDC role for the self-healing-distributed-cache (SHDC) pipeline.
#
# WHY THIS EXISTS (root cause, 2026-09-05):
# This repo was created 2026-09-04, i.e. AFTER GitHub's 2026-07-15 cutoff, so
# GitHub issues IMMUTABLE OIDC `sub` claims:
#   repo:NobleChicken97@141447050/self-healing-distributed-cache@1357349698:ref:refs/heads/master
# The pre-existing roles (github-actions-ecr-role, GitHubActionsECRPush) only
# trust the LEGACY format `repo:NobleChicken97/self-healing-distributed-cache:*`,
# so every AssumeRoleWithWebIdentity fails with AccessDenied (see CloudTrail).
#
# SAFETY RULE: every resource here uses the `shdc-` prefix and touches ONLY this
# project's ECR repo. The shared roles used by other projects (trakPlus, etc.)
# are NOT referenced, modified, or imported here.

data "aws_caller_identity" "shdc_current" {}

locals {
  shdc_github_owner    = "NobleChicken97"
  shdc_github_owner_id = "141447050"              # gh api repos/... --jq '.owner.id'
  shdc_github_repo     = "self-healing-distributed-cache"
  shdc_github_repo_id  = "1357349698"             # gh api repos/... --jq '.id'
  # Immutable sub pattern: allow any ref (branch/tag/PR) of THIS repo only.
  shdc_oidc_sub = "repo:${local.shdc_github_owner}@${local.shdc_github_owner_id}/${local.shdc_github_repo}@${local.shdc_github_repo_id}:*"

  shdc_ecr_repo_arn = "arn:aws:ecr:${var.aws_region}:${data.aws_caller_identity.shdc_current.account_id}:repository/self-healing-cache"
}

resource "aws_iam_role" "shdc_github_actions_ecr" {
  name        = "shdc-github-actions-ecr"
  description = "OIDC role for NobleChicken97/self-healing-distributed-cache GitHub Actions (ECR push). Project prefix: shdc-."

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Principal = {
          Federated = "arn:aws:iam::${data.aws_caller_identity.shdc_current.account_id}:oidc-provider/token.actions.githubusercontent.com"
        }
        Action = "sts:AssumeRoleWithWebIdentity"
        Condition = {
          StringEquals = {
            "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
          }
          StringLike = {
            "token.actions.githubusercontent.com:sub" = local.shdc_oidc_sub
          }
        }
      }
    ]
  })

  tags = {
    Project = "self-healing-distributed-cache"
  }
}

# Least-privilege ECR access scoped to THIS project's repo only
# (the old roles use AmazonEC2ContainerRegistryFullAccess; we do not).
resource "aws_iam_policy" "shdc_ecr_push_pull" {
  name        = "shdc-ecr-push-pull"
  description = "Least-privilege ECR push/pull scoped to the self-healing-cache repository."

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["ecr:GetAuthorizationToken"]
        Resource = "*"
      },
      {
        Effect = "Allow"
        Action = [
          "ecr:BatchCheckLayerAvailability",
          "ecr:BatchGetImage",
          "ecr:CompleteLayerUpload",
          "ecr:GetDownloadUrlForLayer",
          "ecr:InitiateLayerUpload",
          "ecr:PutImage",
          "ecr:UploadLayerPart"
        ]
        Resource = local.shdc_ecr_repo_arn
      }
    ]
  })

  tags = {
    Project = "self-healing-distributed-cache"
  }
}

resource "aws_iam_role_policy_attachment" "shdc_ecr" {
  role       = aws_iam_role.shdc_github_actions_ecr.name
  policy_arn = aws_iam_policy.shdc_ecr_push_pull.arn
}

output "shdc_github_actions_role_arn" {
  description = "Set this ARN as the AWS_ROLE_ARN GitHub secret for this repo's pipeline."
  value       = aws_iam_role.shdc_github_actions_ecr.arn
}
