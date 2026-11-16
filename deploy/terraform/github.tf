# The role that the repository's Deploy workflow (.github/workflows/deploy.yml)
# assumes through GitHub's OIDC provider, so no AWS keys are stored in GitHub.
# Only jobs in var.github_environment of var.github_repository may assume it,
# by the sub claim GitHub gives them (var.github_oidc_subject_prefix).
#
# It can read the account, as a plan must, except the other deployment's part
# of the deploy bucket, and write only what a lobby or VM deploy writes: the
# function's code and settings, this repository's part of the deploy bucket
# (mpl/), and a temporary SSH key for the VM. Anything else, such as new
# alarms or a change to the VM, goes through `scripts/aws.sh apply`: on a
# person's machine, or in the Deploy workflow's apply target, which applies a
# saved plan from main with the apply role from deploy/github_apply_role.py.

data "aws_caller_identity" "current" {}

locals {
  github_deploy = var.github_repository != null
  # The sub claim of the environment's jobs (variables.tf,
  # github_oidc_subject_prefix).
  github_oidc_subject = "${coalesce(var.github_oidc_subject_prefix, "repo:${coalesce(var.github_repository, "-")}")}:environment:${var.github_environment}"
  # This repository's part of the deploy bucket (deploy/deploy_files.py).
  deploy_bucket_prefix = "mpl/"
  # The other deployment's part: the game whose VM this one took over keeps
  # its files (with its secrets), its Terraform state and its saved plans at
  # the bucket's root (deploy/README.md, "Deploying from GitHub Actions").
  # deploy/github_apply_role.py denies the apply role the same.
  other_deployment_prefixes = ["files/", "terraform/", "plans/"]
}

# An account has one OIDC provider for GitHub, which every repository's roles
# trust. Another configuration may own it already, such as the one a game's
# repository applies, so this one only looks it up. If no other project has
# it, create it once by hand (deploy/README.md, "Taking over a running VM",
# step 4).
data "aws_iam_openid_connect_provider" "github" {
  count = local.github_deploy ? 1 : 0
  url   = "https://token.actions.githubusercontent.com"
}

resource "aws_iam_role" "github_deploy" {
  count = local.github_deploy ? 1 : 0
  name  = "${var.name}-github-deploy"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Federated = data.aws_iam_openid_connect_provider.github[0].arn }
      Action    = "sts:AssumeRoleWithWebIdentity"
      Condition = {
        StringEquals = {
          "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
          "token.actions.githubusercontent.com:sub" = local.github_oidc_subject
        }
      }
    }]
  })
}

# Every plan refreshes every resource, across Lightsail, Lambda, IAM, logs,
# alarms, SNS, ACM, CloudFront and the backups bucket. The bucket's policy
# keeps the backups themselves from this role (backups.tf).
resource "aws_iam_role_policy_attachment" "github_deploy_read" {
  count      = local.github_deploy ? 1 : 0
  role       = aws_iam_role.github_deploy[0].name
  policy_arn = "arn:aws:iam::aws:policy/ReadOnlyAccess"
}

resource "aws_iam_role_policy" "github_deploy" {
  count = local.github_deploy ? 1 : 0
  name  = "deploy"
  role  = aws_iam_role.github_deploy[0].id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "PublishTheLobby"
        Effect = "Allow"
        Action = [
          "lambda:UpdateFunctionCode",
          "lambda:UpdateFunctionConfiguration",
          "lambda:TagResource",
          "lambda:UntagResource",
        ]
        Resource = "arn:aws:lambda:${var.region}:${data.aws_caller_identity.current.account_id}:function:${var.name}"
      },
      {
        Sid      = "KeepTheFunctionsRole"
        Effect   = "Allow"
        Action   = "iam:PassRole"
        Resource = aws_iam_role.lambda.arn
        Condition = {
          StringEquals = { "iam:PassedToService" = "lambda.amazonaws.com" }
        }
      },
      {
        Sid      = "ListTheDeployBucket"
        Effect   = "Allow"
        Action   = "s3:ListBucket"
        Resource = "arn:aws:s3:::${var.deploy_bucket}"
      },
      {
        # The state, its lock file, deploy/local/'s files, the saved plans
        # and the tenants' settings, all under mpl/. Another deployment's
        # objects in the same bucket are left alone.
        Sid      = "SyncItsPartOfTheDeployBucket"
        Effect   = "Allow"
        Action   = ["s3:GetObject", "s3:PutObject"]
        Resource = "arn:aws:s3:::${var.deploy_bucket}/${local.deploy_bucket_prefix}*"
      },
      {
        # Nothing else is deleted: Terraform removes its lock, and a plan run
        # removes saved plans that nobody applied (deploy/deploy_files.py).
        Sid    = "ReleaseTheStateLockAndOldPlans"
        Effect = "Allow"
        Action = "s3:DeleteObject"
        Resource = [
          "arn:aws:s3:::${var.deploy_bucket}/${local.deploy_bucket_prefix}terraform/terraform.tfstate.tflock",
          "arn:aws:s3:::${var.deploy_bucket}/${local.deploy_bucket_prefix}plans/*",
        ]
      },
      {
        # ReadOnlyAccess reads every bucket, so an explicit Deny keeps the
        # other deployment's secrets from this role. s3:GetObject* is every
        # read of an object: its content, the earlier versions the bucket
        # keeps, their checksums (GetObjectAttributes), tags and ACLs. None of
        # them is this repository's to read. Listing the keys stays allowed,
        # and nothing under mpl/ is denied.
        Sid      = "KeepOutOfTheOtherDeploymentsPart"
        Effect   = "Deny"
        Action   = "s3:GetObject*"
        Resource = [for prefix in local.other_deployment_prefixes : "arn:aws:s3:::${var.deploy_bucket}/${prefix}*"]
      },
      {
        Sid      = "LogInToTheVm"
        Effect   = "Allow"
        Action   = "lightsail:GetInstanceAccessDetails"
        Resource = "*"
      },
    ]
  })
}
