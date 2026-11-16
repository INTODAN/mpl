"""Tests for the role GitHub Actions applies Terraform with, with a fake AWS CLI.

Run with: python3 -I -m unittest discover -s deploy -p 'test_*.py'
"""

import contextlib
import fnmatch
import io
import json
import unittest
from unittest import mock

import github_apply_role
from deploy_files import AwsError

ACCOUNT = "123456789012"
REPOSITORY = "example/mpl"
BUCKET = "shared-deploy-123456789012"


class FakeAws:
    """Records `aws` calls and answers the few that return something."""

    def __init__(self, role_exists=False, provider_exists=True):
        self.role_exists = role_exists
        self.provider_exists = provider_exists
        self.calls = []

    def text(self, *arguments):
        self.calls.append(arguments)
        if arguments[:2] == ("sts", "get-caller-identity"):
            return ACCOUNT + "\n"
        return ""

    def json(self, *arguments):
        self.calls.append(arguments)
        if arguments[:2] == ("iam", "get-open-id-connect-provider") and not self.provider_exists:
            raise AwsError("NoSuchEntity")
        if arguments[:2] == ("iam", "create-role") and self.role_exists:
            raise AwsError("An error occurred (EntityAlreadyExists) when calling the CreateRole operation")
        return {}

    def operations(self):
        return [call[1] for call in self.calls]

    def argument(self, operation, name):
        call = next(call for call in self.calls if call[1] == operation)
        return call[call.index(name) + 1]


# The object reads that ReadOnlyAccess allows with s3:Get*.
OBJECT_READS = [
    "s3:GetObject", "s3:GetObjectVersion", "s3:GetObjectAttributes", "s3:GetObjectVersionAttributes",
    "s3:GetObjectTorrent", "s3:GetObjectVersionTorrent", "s3:GetObjectVersionForReplication",
    "s3:GetObjectTagging", "s3:GetObjectVersionTagging", "s3:GetObjectAcl", "s3:GetObjectVersionAcl",
    "s3:GetObjectRetention", "s3:GetObjectLegalHold",
]


def listed(value):
    return value if isinstance(value, list) else [value]


def matches(statement, action, resource):
    """Whether statement names action and resource, with IAM's wildcards."""
    return (any(fnmatch.fnmatchcase(action, pattern) for pattern in listed(statement["Action"]))
            and any(fnmatch.fnmatchcase(resource, pattern) for pattern in listed(statement["Resource"])))


def denied(policy, action, resource):
    return any(statement["Effect"] == "Deny" and matches(statement, action, resource)
               for statement in policy["Statement"])


def run(fake, arguments):
    output, errors = io.StringIO(), io.StringIO()
    with mock.patch.object(github_apply_role, "aws", fake.text), \
            mock.patch.object(github_apply_role, "aws_json", fake.json), \
            contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
        status = github_apply_role.main(arguments)
    return status, output.getvalue(), errors.getvalue()


class PolicyTest(unittest.TestCase):
    def test_trusts_only_the_main_only_production_environment(self):
        trust = github_apply_role.trust_policy(ACCOUNT, REPOSITORY)["Statement"]
        self.assertEqual(len(trust), 1)
        self.assertEqual(trust[0]["Principal"], {
            "Federated": f"arn:aws:iam::{ACCOUNT}:oidc-provider/token.actions.githubusercontent.com"})
        self.assertEqual(trust[0]["Condition"], {"StringEquals": {
            "token.actions.githubusercontent.com:aud": "sts.amazonaws.com",
            "token.actions.githubusercontent.com:sub": f"repo:{REPOSITORY}:environment:production",
        }})

    def test_trusts_the_immutable_subject_when_given(self):
        prefix = "repo:example@123/mpl@456"
        trust = github_apply_role.trust_policy(ACCOUNT, REPOSITORY, prefix)["Statement"]
        self.assertEqual(trust[0]["Condition"]["StringEquals"]["token.actions.githubusercontent.com:sub"],
                         "repo:example@123/mpl@456:environment:production")

    def test_matches_a_subject_prefix_to_its_repository(self):
        for prefix in ("repo:example/mpl", "repo:example@123/mpl@456"):
            self.assertTrue(github_apply_role.subject_prefix_matches(prefix, REPOSITORY), prefix)
        for prefix in ("repo:other@123/mpl@456", "repo:example@123/other@456", "example@123/mpl@456",
                       "repo:example@123/mpl@456:environment:production", "repo:example@x/mpl"):
            self.assertFalse(github_apply_role.subject_prefix_matches(prefix, REPOSITORY), prefix)

    def test_changes_only_the_deployments_resources_and_never_itself(self):
        statements = {statement["Sid"]: statement
                      for statement in github_apply_role.permissions_policy(ACCOUNT, BUCKET)["Statement"]}
        self.assertEqual(set(statements["ManageTheDeployment"]["Action"]), {
            "lightsail:*", "lambda:*", "logs:*", "cloudwatch:*", "sns:*", "acm:*", "cloudfront:*"})
        self.assertEqual(statements["ManageItsBuckets"]["Resource"], ["arn:aws:s3:::mpl-*", "arn:aws:s3:::mpl-*/*"])
        # deploy/terraform/backups.tf names its bucket <name>-backups-<account>.
        self.assertTrue(fnmatch.fnmatchcase(f"arn:aws:s3:::mpl-backups-{ACCOUNT}",
                                            statements["ManageItsBuckets"]["Resource"][0]))
        # The shared deploy bucket: only this repository's part.
        self.assertEqual(statements["UseItsPartOfTheDeployBucket"]["Resource"], f"arn:aws:s3:::{BUCKET}/mpl/*")
        self.assertFalse(fnmatch.fnmatchcase(f"arn:aws:s3:::{BUCKET}/terraform/terraform.tfstate",
                                             statements["UseItsPartOfTheDeployBucket"]["Resource"]))
        self.assertTrue(fnmatch.fnmatchcase(f"arn:aws:s3:::{BUCKET}/mpl/terraform/terraform.tfstate.tflock",
                                            statements["UseItsPartOfTheDeployBucket"]["Resource"]))
        self.assertEqual(statements["ManageItsRolesAndUsers"]["Resource"], [
            f"arn:aws:iam::{ACCOUNT}:role/mpl-*",
            f"arn:aws:iam::{ACCOUNT}:user/mpl-*",
        ])
        # deploy/terraform/ names its roles and its user <name>-...
        for name in ("role/mpl-lambda", "role/mpl-github-deploy", "user/mpl-database-backups"):
            self.assertTrue(any(fnmatch.fnmatchcase(f"arn:aws:iam::{ACCOUNT}:{name}", pattern)
                                for pattern in statements["ManageItsRolesAndUsers"]["Resource"]), name)
        self.assertEqual(statements["LeaveThisRoleAlone"], {
            "Sid": "LeaveThisRoleAlone", "Effect": "Deny", "Action": "iam:*",
            "Resource": f"arn:aws:iam::{ACCOUNT}:role/mpl-github-apply"})
        allowed = [statement for statement in statements.values() if statement["Effect"] == "Allow"]
        self.assertFalse(any(statement["Resource"] == "*" and "iam:*" in statement["Action"] for statement in allowed),
                         "IAM must stay limited to the deployment's own roles and users")

    def test_cant_read_the_other_deployments_part_of_the_deploy_bucket(self):
        policy = github_apply_role.permissions_policy(ACCOUNT, BUCKET)
        statements = {statement["Sid"]: statement for statement in policy["Statement"]}
        self.assertEqual(statements["KeepOutOfTheOtherDeploymentsPart"], {
            "Sid": "KeepOutOfTheOtherDeploymentsPart", "Effect": "Deny", "Action": "s3:GetObject*",
            "Resource": [f"arn:aws:s3:::{BUCKET}/files/*", f"arn:aws:s3:::{BUCKET}/terraform/*",
                         f"arn:aws:s3:::{BUCKET}/plans/*"]})
        # The other deployment's secrets, settings, state, lock and plans, in
        # every version the bucket keeps, and their checksums.
        for key in ("files/secrets/admin_api_key", "files/lambda.env", "terraform/terraform.tfstate",
                    "terraform/terraform.tfstate.tflock", "plans/123/plan.tfplan"):
            for action in OBJECT_READS:
                self.assertTrue(denied(policy, action, f"arn:aws:s3:::{BUCKET}/{key}"), f"{action} {key}")
        # Nothing of this repository's, including the tenants' settings that
        # the game reads with its own roles.
        for key in ("mpl/files/vm.yml", "mpl/files/secrets/postgres_admin_password", "mpl/terraform/terraform.tfstate",
                    "mpl/plans/123/plan.tfplan", "mpl/tenants/island-battles/tenant.env", "mpl/vm-owner"):
            resource = f"arn:aws:s3:::{BUCKET}/{key}"
            for action in OBJECT_READS:
                self.assertFalse(denied(policy, action, resource), f"{action} {key}")
            self.assertTrue(matches(statements["UseItsPartOfTheDeployBucket"], "s3:GetObject", resource), key)
        # Listing the bucket, and other buckets' objects, are left alone.
        self.assertFalse(denied(policy, "s3:ListBucket", f"arn:aws:s3:::{BUCKET}"))
        self.assertFalse(denied(policy, "s3:GetObject", f"arn:aws:s3:::mpl-backups-{ACCOUNT}/files/x"))

    def test_prints_the_policies_without_aws(self):
        fake = FakeAws()
        status, output, _ = run(fake, ["policies", "--account", ACCOUNT, "--repository", REPOSITORY,
                                       "--deploy-bucket", BUCKET])
        self.assertEqual(status, 0)
        self.assertEqual(fake.calls, [])
        printed = json.loads(output)
        self.assertEqual(printed["managed"], ["arn:aws:iam::aws:policy/ReadOnlyAccess"])
        self.assertEqual(printed["trust"], github_apply_role.trust_policy(ACCOUNT, REPOSITORY))


class CreateTest(unittest.TestCase):
    def test_creates_the_role_with_both_policies(self):
        fake = FakeAws()
        status, output, _ = run(fake, ["create", "--repository", REPOSITORY, "--deploy-bucket", BUCKET])

        self.assertEqual(status, 0)
        self.assertEqual(output.strip(), f"arn:aws:iam::{ACCOUNT}:role/mpl-github-apply")
        self.assertEqual(fake.operations(), [
            "get-caller-identity", "get-open-id-connect-provider", "create-role", "attach-role-policy",
            "put-role-policy"])
        self.assertEqual(json.loads(fake.argument("create-role", "--assume-role-policy-document")),
                         github_apply_role.trust_policy(ACCOUNT, REPOSITORY))
        self.assertEqual(fake.argument("attach-role-policy", "--policy-arn"), "arn:aws:iam::aws:policy/ReadOnlyAccess")
        self.assertEqual(json.loads(fake.argument("put-role-policy", "--policy-document")),
                         github_apply_role.permissions_policy(ACCOUNT, BUCKET))

    def test_creates_the_role_trusting_the_subject_prefix(self):
        fake = FakeAws(role_exists=True)
        prefix = "repo:example@123/mpl@456"
        status, _, _ = run(fake, ["create", "--repository", REPOSITORY, "--deploy-bucket", BUCKET,
                                  "--subject-prefix", prefix])

        self.assertEqual(status, 0)
        self.assertEqual(json.loads(fake.argument("update-assume-role-policy", "--policy-document")),
                         github_apply_role.trust_policy(ACCOUNT, REPOSITORY, prefix))

    def test_rejects_another_repositorys_subject_prefix(self):
        fake = FakeAws()
        status, _, errors = run(fake, ["create", "--repository", REPOSITORY, "--deploy-bucket", BUCKET,
                                       "--subject-prefix", "repo:other@123/mpl@456"])
        self.assertEqual(status, 2)
        self.assertEqual(fake.calls, [])
        self.assertIn("sub_claim_prefix", errors)

    def test_updates_an_existing_roles_trust_and_policies(self):
        fake = FakeAws(role_exists=True)
        status, _, errors = run(fake, ["create", "--repository", REPOSITORY, "--deploy-bucket", BUCKET])

        self.assertEqual(status, 0)
        self.assertIn("update-assume-role-policy", fake.operations())
        self.assertIn("put-role-policy", fake.operations())
        self.assertIn("exists", errors)

    def test_needs_githubs_oidc_provider(self):
        fake = FakeAws(provider_exists=False)
        status, _, errors = run(fake, ["create", "--repository", REPOSITORY, "--deploy-bucket", BUCKET])

        self.assertEqual(status, 1)
        self.assertNotIn("create-role", fake.operations())
        self.assertIn("OIDC provider is missing", errors)

    def test_rejects_a_repository_that_isnt_owner_and_name(self):
        fake = FakeAws()
        status, _, _ = run(fake, ["create", "--repository", "https://github.com/example/mpl", "--deploy-bucket", BUCKET])
        self.assertEqual(status, 2)
        self.assertEqual(fake.calls, [])

    def test_needs_the_deploy_bucket(self):
        fake = FakeAws()
        status, _, _ = run(fake, ["create", "--repository", REPOSITORY])
        self.assertEqual(status, 2)
        self.assertEqual(fake.calls, [])


if __name__ == "__main__":
    unittest.main()
