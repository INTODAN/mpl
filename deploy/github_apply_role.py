"""Create the role that lets GitHub Actions apply Terraform from main.

The Deploy workflow's `apply` target applies a plan that its `plan` target
saved. Its jobs run in the `production` environment, which the repository's
settings limit to main, and this role trusts only that environment's jobs,
through GitHub's OIDC provider. So only merged code ever gets it: merging the
pull request is the approval (deploy/README.md, "Deploying from GitHub
Actions").

The role reads the whole account (ReadOnlyAccess), as a plan must, and changes
only what deploy/terraform/ manages: Lightsail (the VM, taken over or new),
Lambda, CloudWatch logs and alarms, SNS, ACM and CloudFront, buckets named
mpl-*, this repository's part of the shared deploy bucket (mpl/), and IAM
roles and users named mpl-*. It can't change itself. Because it can write IAM
policies for mpl-* roles, treat it as close to administrator of the account:
reviewing what merges to main is the control.

It lives outside Terraform, like the deploy bucket, so a Terraform run can't
widen the role that applies it. Run this again after changing it here.

Usage, from the repository root, with credentials that can manage IAM:

    python3 -I deploy/github_apply_role.py create --repository <owner>/<name> --deploy-bucket <bucket>
    python3 -I deploy/github_apply_role.py policies --account <id> --repository <owner>/<name> --deploy-bucket <bucket>

`policies` prints the trust and permission policies without calling AWS.
"""

import json
import pathlib
import re
import sys

# `python3 -I` leaves the script's directory off the path.
sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
from deploy_files import PREFIX, AwsError, aws, aws_json  # noqa: E402

# deploy/terraform/variables.tf's default name, which prefixes everything the
# configuration creates.
NAME_PREFIX = "mpl-"
ROLE_NAME = NAME_PREFIX + "github-apply"
# The deploy role (deploy/terraform/github.tf) trusts the same environment.
ENVIRONMENT = "production"
OIDC_HOST = "token.actions.githubusercontent.com"
READ_POLICY_ARN = "arn:aws:iam::aws:policy/ReadOnlyAccess"
PERMISSIONS_POLICY_NAME = "apply-the-deployment"
# What deploy/terraform/ creates or changes, besides S3 and IAM. The account's
# cost budgets aren't this configuration's (observability.tf).
MANAGED_SERVICES = ["lightsail", "lambda", "logs", "cloudwatch", "sns", "acm", "cloudfront"]


def oidc_provider_arn(account):
    return f"arn:aws:iam::{account}:oidc-provider/{OIDC_HOST}"


def role_arn(account):
    return f"arn:aws:iam::{account}:role/{ROLE_NAME}"


def trust_policy(account, repository):
    return {
        "Version": "2012-10-17",
        "Statement": [{
            "Effect": "Allow",
            "Principal": {"Federated": oidc_provider_arn(account)},
            "Action": "sts:AssumeRoleWithWebIdentity",
            "Condition": {"StringEquals": {
                f"{OIDC_HOST}:aud": "sts.amazonaws.com",
                f"{OIDC_HOST}:sub": f"repo:{repository}:environment:{ENVIRONMENT}",
            }},
        }],
    }


def permissions_policy(account, deploy_bucket):
    iam = f"arn:aws:iam::{account}"
    return {
        "Version": "2012-10-17",
        "Statement": [
            {
                "Sid": "ManageTheDeployment",
                "Effect": "Allow",
                "Action": [f"{service}:*" for service in MANAGED_SERVICES],
                "Resource": "*",
            },
            {
                # The backups bucket.
                "Sid": "ManageItsBuckets",
                "Effect": "Allow",
                "Action": "s3:*",
                "Resource": [f"arn:aws:s3:::{NAME_PREFIX}*", f"arn:aws:s3:::{NAME_PREFIX}*/*"],
            },
            {
                # Terraform's state and lock, deploy/local/'s files, the saved
                # plans and the tenants' settings. Another deployment's part of
                # the shared bucket is left alone.
                "Sid": "UseItsPartOfTheDeployBucket",
                "Effect": "Allow",
                "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"],
                "Resource": f"arn:aws:s3:::{deploy_bucket}/{PREFIX}*",
            },
            {
                "Sid": "ListTheDeployBucket",
                "Effect": "Allow",
                "Action": "s3:ListBucket",
                "Resource": f"arn:aws:s3:::{deploy_bucket}",
            },
            {
                # The account's GitHub provider is only read (github.tf), which
                # ReadOnlyAccess allows.
                "Sid": "ManageItsRolesAndUsers",
                "Effect": "Allow",
                "Action": "iam:*",
                "Resource": [f"{iam}:role/{NAME_PREFIX}*", f"{iam}:user/{NAME_PREFIX}*"],
            },
            {
                "Sid": "LeaveThisRoleAlone",
                "Effect": "Deny",
                "Action": "iam:*",
                "Resource": role_arn(account),
            },
        ],
    }


def create(repository, deploy_bucket):
    account = aws("sts", "get-caller-identity", "--query", "Account", "--output", "text").strip()
    try:
        aws_json("iam", "get-open-id-connect-provider", "--open-id-connect-provider-arn", oidc_provider_arn(account))
    except AwsError as error:
        raise AwsError(f"{error}\nGitHub's OIDC provider is missing. Create it once for the account "
                       "(deploy/README.md, \"Deploying from GitHub Actions\").") from error
    trust = json.dumps(trust_policy(account, repository))
    try:
        aws_json("iam", "create-role", "--role-name", ROLE_NAME, "--assume-role-policy-document", trust,
                 "--max-session-duration", "3600",
                 "--description", f"GitHub Actions jobs in {repository}'s {ENVIRONMENT} environment apply Terraform",
                 "--tags", "Key=Project,Value=mpl", "Key=ManagedBy,Value=github_apply_role.py")
        print(f"Created the role {ROLE_NAME}.", file=sys.stderr)
    except AwsError as error:
        if "EntityAlreadyExists" not in str(error):
            raise
        aws("iam", "update-assume-role-policy", "--role-name", ROLE_NAME, "--policy-document", trust)
        print(f"{ROLE_NAME} exists. Updating its policies.", file=sys.stderr)
    aws("iam", "attach-role-policy", "--role-name", ROLE_NAME, "--policy-arn", READ_POLICY_ARN)
    aws("iam", "put-role-policy", "--role-name", ROLE_NAME, "--policy-name", PERMISSIONS_POLICY_NAME,
        "--policy-document", json.dumps(permissions_policy(account, deploy_bucket)))
    return role_arn(account)


def main(arguments):
    usage = ("Usage: github_apply_role.py create --repository <owner>/<name> --deploy-bucket <bucket>\n"
             "       github_apply_role.py policies --account <id> --repository <owner>/<name> --deploy-bucket <bucket>")
    if not arguments or arguments[0] not in ("create", "policies"):
        print(usage, file=sys.stderr)
        return 2
    options = dict(zip(arguments[1::2], arguments[2::2]))
    repository = options.get("--repository", "")
    deploy_bucket = options.get("--deploy-bucket", "")
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository) or \
            not re.fullmatch(r"[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]", deploy_bucket):
        print(usage, file=sys.stderr)
        return 2
    if arguments[0] == "policies":
        account = options.get("--account", "")
        if not re.fullmatch(r"\d{12}", account):
            print(usage, file=sys.stderr)
            return 2
        print(json.dumps({"trust": trust_policy(account, repository),
                          "permissions": permissions_policy(account, deploy_bucket),
                          "managed": [READ_POLICY_ARN]}, indent=2))
        return 0
    try:
        arn = create(repository, deploy_bucket)
    except AwsError as error:
        print(error, file=sys.stderr)
        return 1
    print(arn)
    print(f"Only jobs in the {ENVIRONMENT} environment can assume it. Keep that environment limited to main "
          "(deploy/README.md, \"Deploying from GitHub Actions\").", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
