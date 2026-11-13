"""Tests for check_deploy_plan.py with synthetic plans, in the shape that
`terraform show -json` gives them (an import carries change.importing).

Run from the repository root:

    python3 -I -m unittest discover -s deploy -p 'test_*.py'
"""

import contextlib
import io
import json
import unittest

import check_deploy_plan


def change(address, actions, before_name=None, before=None, importing=None):
    resource_type = address.split(".")[0]
    if before is None and before_name is not None:
        before = {"name": before_name}
    planned = {"actions": actions, "before": before}
    if importing is not None:
        planned["importing"] = {"id": importing}
    return {
        "address": address,
        "mode": "managed",
        "type": resource_type,
        "change": planned,
    }


def run(mode, *changes):
    """Run the script's main on a plan, returning (exit code, stdout, stderr)."""
    plan = {"resource_changes": list(changes)}
    stdout, stderr = io.StringIO(), io.StringIO()
    stdin = io.StringIO(json.dumps(plan))
    with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
        original_stdin = check_deploy_plan.sys.stdin
        check_deploy_plan.sys.stdin = stdin
        try:
            code = check_deploy_plan.main([mode])
        finally:
            check_deploy_plan.sys.stdin = original_stdin
    return code, stdout.getvalue(), stderr.getvalue()


FUNCTION_CREATE = change("aws_lambda_function.lobby", ["create"])
FUNCTION_UPDATE = change("aws_lambda_function.lobby", ["update"], "mpl")
INSTANCE_REPLACE = change('aws_lightsail_instance.vm["game-2"]', ["delete", "create"], "game-2")
INSTANCE_DELETE = change('aws_lightsail_instance.vm["game-1"]', ["delete"], "game-1")
INSTANCE_NO_OP = change('aws_lightsail_instance.vm["game-1"]', ["no-op"], "game-1")
PORTS_UPDATE = change('aws_lightsail_instance_public_ports.vm["game-1"]', ["update"])
PORTS_CREATE = change('aws_lightsail_instance_public_ports.vm["game-1"]', ["create"])
ATTACHMENT_REPLACE = change("aws_lightsail_static_ip_attachment.vm", ["delete", "create"])
STATIC_IP_DELETE = change("aws_lightsail_static_ip.vm", ["delete"], "game-vm")
URL_REPLACE = change("aws_lambda_function_url.lobby", ["create", "delete"])
LOG_GROUP_CREATE = change("aws_cloudwatch_log_group.lobby", ["create"])
CERTIFICATE_CREATE = change("aws_acm_certificate.lobby", ["create"])
DISTRIBUTION_CREATE = change("aws_cloudfront_distribution.lobby", ["create"])
DISTRIBUTION_UPDATE = change("aws_cloudfront_distribution.lobby", ["update"])
DISTRIBUTION_REPLACE = change("aws_cloudfront_distribution.lobby", ["delete", "create"],
                              before={"domain_name": "d111111abcdef8.cloudfront.net"})

# Taking over a running VM: each import in the plan, as Terraform shows it.
INSTANCE_IMPORT = change('aws_lightsail_instance.vm["game-1"]', ["no-op"], "game-1", importing="game-1")
STATIC_IP_IMPORT = change("aws_lightsail_static_ip.vm", ["no-op"], "game-vm", importing="game-vm")
ATTACHMENT_IMPORT = change("aws_lightsail_static_ip_attachment.vm", ["no-op"], importing="game-vm")
TAKEOVER = (INSTANCE_IMPORT, STATIC_IP_IMPORT, ATTACHMENT_IMPORT)
# The rest of the first apply: the lobby's own resources, all new.
FIRST_APPLY = (FUNCTION_CREATE, LOG_GROUP_CREATE, CERTIFICATE_CREATE, DISTRIBUTION_CREATE,
               change("aws_lambda_function_url.lobby", ["create"]), change("aws_s3_bucket.backups", ["create"]),
               change("aws_iam_access_key.backups", ["create"]), change("aws_sns_topic.alerts", ["create"]))


class DeployModeTest(unittest.TestCase):
    def test_allows_updating_the_function_and_creating_resources(self):
        code, _, stderr = run("deploy", FUNCTION_UPDATE, LOG_GROUP_CREATE, INSTANCE_NO_OP)
        self.assertEqual(code, 0, stderr)

    def test_refuses_any_lightsail_change(self):
        code, _, stderr = run("deploy", FUNCTION_UPDATE, PORTS_UPDATE)
        self.assertEqual(code, 1)
        self.assertIn("aws_lightsail_instance_public_ports", stderr)

    def test_refuses_creating_the_certificate_and_changing_the_distribution(self):
        code, _, stderr = run("deploy", FUNCTION_UPDATE, CERTIFICATE_CREATE, DISTRIBUTION_UPDATE)
        self.assertEqual(code, 1)
        self.assertIn("aws_acm_certificate.lobby: create (the lobby's certificate)", stderr)
        self.assertIn("aws_cloudfront_distribution.lobby: update (the lobby's distribution)", stderr)

    def test_refuses_replacing_the_function_url(self):
        code, _, stderr = run("deploy", FUNCTION_UPDATE, URL_REPLACE)
        self.assertEqual(code, 1)
        self.assertIn("aws_lambda_function_url.lobby: create/delete", stderr)

    def test_refuses_an_import_even_one_that_changes_nothing(self):
        # Imports go through an apply that a person reads.
        code, _, stderr = run("deploy", FUNCTION_UPDATE, INSTANCE_IMPORT)
        self.assertEqual(code, 1)
        self.assertIn('aws_lightsail_instance.vm["game-1"]: import', stderr)

    def test_a_plan_without_changes_passes(self):
        self.assertEqual(run("deploy", INSTANCE_NO_OP)[0], 0)


class ImportsModeTest(unittest.TestCase):
    def test_the_takeover_imports_the_vm_with_no_change(self):
        code, stdout, stderr = run("imports", *TAKEOVER, *FIRST_APPLY)
        self.assertEqual(code, 0, stderr)
        self.assertEqual(stdout, "")
        self.assertIn("imports 3 resources, each with no change", stderr)
        self.assertIn("aws_lightsail_static_ip.vm", stderr)

    def test_the_takeover_destroys_nothing_and_migrates_once_the_settings_exist(self):
        self.assertEqual(run("apply", *TAKEOVER, *FIRST_APPLY)[:2], (0, ""))
        self.assertEqual(run("publishes-lobby", *TAKEOVER, *FIRST_APPLY)[1], "yes\n")

    def test_refuses_an_import_that_updates(self):
        # Such as other tags than the instance has.
        updated = change('aws_lightsail_instance.vm["game-1"]', ["update"], "game-1", importing="game-1")
        code, _, stderr = run("imports", updated, STATIC_IP_IMPORT, ATTACHMENT_IMPORT, *FIRST_APPLY)
        self.assertEqual(code, 1)
        self.assertIn('aws_lightsail_instance.vm["game-1"]: update (an import must change nothing)', stderr)
        self.assertIn("Taking over a running VM", stderr)

    def test_refuses_an_import_that_replaces(self):
        # Such as another bundle or zone than the instance runs with.
        replaced = change('aws_lightsail_instance.vm["game-1"]', ["delete", "create"], "game-1", importing="game-1")
        code, _, stderr = run("imports", replaced, STATIC_IP_IMPORT, ATTACHMENT_IMPORT)
        self.assertEqual(code, 1)
        self.assertIn('aws_lightsail_instance.vm["game-1"]: delete/create (an import must change nothing)', stderr)

    def test_refuses_a_static_ip_attachment_that_moves(self):
        moved = change("aws_lightsail_static_ip_attachment.vm", ["delete", "create"], importing="game-vm")
        code, _, stderr = run("imports", INSTANCE_IMPORT, STATIC_IP_IMPORT, moved)
        self.assertEqual(code, 1)
        self.assertIn("aws_lightsail_static_ip_attachment.vm: delete/create", stderr)

    def test_refuses_creating_the_firewall_of_a_vm_it_takes_over(self):
        # Lightsail can't import a firewall, and creating one replaces the
        # running VM's rules.
        code, _, stderr = run("imports", *TAKEOVER, PORTS_CREATE)
        self.assertEqual(code, 1)
        self.assertIn('aws_lightsail_instance_public_ports.vm["game-1"]: create (a Lightsail resource, in a plan '
                      "that takes over a VM)", stderr)
        self.assertIn("manage_vm_firewall = false", stderr)

    def test_refuses_creating_another_instance_while_taking_over(self):
        created = change('aws_lightsail_instance.vm["game-2"]', ["create"])
        code, _, stderr = run("imports", *TAKEOVER, created)
        self.assertEqual(code, 1)
        self.assertIn('aws_lightsail_instance.vm["game-2"]: create', stderr)

    def test_a_plan_without_imports_may_change_the_vm(self):
        # Once the VM is in the state, a plan imports nothing, and changes to
        # it go through the apply mode's confirmations instead.
        code, stdout, stderr = run("imports", PORTS_CREATE, INSTANCE_REPLACE, FUNCTION_UPDATE)
        self.assertEqual((code, stdout, stderr), (0, "", ""))

    def test_imports_of_other_resources_must_change_nothing_too(self):
        updated = change("aws_s3_bucket.backups", ["update"], "mpl-backups-1", importing="mpl-backups-1")
        code, _, stderr = run("imports", updated)
        self.assertEqual(code, 1)
        self.assertIn("aws_s3_bucket.backups: update (an import must change nothing)", stderr)


class ApplyModeTest(unittest.TestCase):
    def test_names_each_destroyed_instance_static_ip_and_distribution(self):
        code, stdout, stderr = run("apply", INSTANCE_REPLACE, INSTANCE_DELETE, STATIC_IP_DELETE, DISTRIBUTION_REPLACE,
                                   FUNCTION_UPDATE)
        self.assertEqual(code, 0)
        self.assertEqual(stdout.splitlines(), ["game-2", "game-1", "game-vm", "d111111abcdef8.cloudfront.net"])
        self.assertIn("replaces the instance", stderr)
        self.assertIn("deletes the static IP", stderr)

    def test_moving_the_static_ip_destroys_nothing_hard_to_get_back(self):
        self.assertEqual(run("apply", ATTACHMENT_REPLACE)[:2], (0, ""))

    def test_no_ops_destroy_nothing(self):
        self.assertEqual(run("apply", *TAKEOVER, INSTANCE_NO_OP)[:2], (0, ""))


class PublishesLobbyTest(unittest.TestCase):
    def test_an_update_of_the_function_publishes_it(self):
        self.assertEqual(run("publishes-lobby", FUNCTION_UPDATE)[1], "yes\n")

    def test_moving_the_static_ip_doesnt(self):
        self.assertEqual(run("publishes-lobby", ATTACHMENT_REPLACE, INSTANCE_NO_OP)[1], "no\n")


class UsageTest(unittest.TestCase):
    def test_refuses_an_unknown_mode(self):
        self.assertEqual(check_deploy_plan.main(["publishes-backend"]), 2)


if __name__ == "__main__":
    unittest.main()
