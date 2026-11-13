"""Tests for deploy_files.py's sync, against an in-memory bucket.

Run from the repository root:

    python3 -I -m unittest discover -s deploy -p 'test_*.py'
"""

import datetime
import hashlib
import os
import pathlib
import shutil
import tempfile
import unittest

import deploy_files

BUCKET = "shared-deploy-123456789012"


def md5(content):
    return hashlib.md5(content.encode(), usedforsecurity=False).hexdigest()


class FakeStore:
    """The bucket's mpl/files/, with the same conditional uploads as S3, and
    any other key the bucket holds, with its last-modified time."""

    def __init__(self, files=None, state=False, objects=None):
        self.files = dict(files or {})
        self.state = state
        self.uploads = []
        # {key: (content, last modified)} outside mpl/files/.
        self.objects = dict(objects or {})

    def list(self, prefix=deploy_files.FILES_PREFIX):
        if prefix == deploy_files.FILES_PREFIX:
            return {path: md5(content) for path, content in self.files.items()}
        return {key[len(prefix):]: md5(content) for key, (content, _) in self.objects.items()
                if key.startswith(prefix)}

    def list_dated(self, prefix):
        return {key: modified for key, (_, modified) in self.objects.items() if key.startswith(prefix)}

    def put(self, key, source):
        self.objects[key] = (pathlib.Path(source).read_text(), NOW)

    def read(self, key):
        return self.objects[key][0] if key in self.objects else None

    def delete(self, key):
        del self.objects[key]

    def download(self, path, destination):
        pathlib.Path(destination).write_text(self.files[path])

    def upload(self, path, source, expected):
        current = self.files.get(path)
        if (None if current is None else md5(current)) != expected:
            raise deploy_files.PreconditionFailed("412")
        self.files[path] = pathlib.Path(source).read_text()
        self.uploads.append(path)

    def has_state(self):
        return self.state


class SyncTest(unittest.TestCase):
    def setUp(self):
        self.directory = pathlib.Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.directory)

    def write(self, path, content):
        target = self.directory / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content)

    def read(self, path):
        return (self.directory / path).read_text()

    def pull(self, store, force=False):
        return deploy_files.pull(store, self.directory, BUCKET, force, log=lambda _: None)

    def push(self, store, force=False):
        return deploy_files.push(store, self.directory, BUCKET, force, log=lambda _: None)

    def test_a_new_machine_pulls_everything(self):
        store = FakeStore({"vm.yml": "a", "secrets/centrifugo_api_key": "k"})
        self.assertEqual(self.pull(store), [])
        self.assertEqual(self.read("vm.yml"), "a")
        self.assertEqual(self.read("secrets/centrifugo_api_key"), "k")
        self.assertEqual((self.directory / "secrets/centrifugo_api_key").stat().st_mode & 0o777, 0o600)

    def test_the_first_push_uploads_everything_shared(self):
        self.write("vm.yml", "a")
        self.write("secrets/postgres_app_password", "p")
        self.write("terraform/scripts-aws.tfplan", "plan")
        self.write("lambda/bootstrap", "binary")
        self.write("dumps/lobby.dump", "dump")
        self.write("tenants/example-game/tenant.env", "published elsewhere")
        store = FakeStore()
        self.assertEqual(self.push(store), [])
        self.assertEqual(store.files, {"vm.yml": "a", "secrets/postgres_app_password": "p"})

    def test_pull_takes_a_change_made_only_in_the_bucket(self):
        store = FakeStore({"vm.yml": "a"})
        self.pull(store)
        store.files["vm.yml"] = "b"
        self.assertEqual(self.pull(store), [])
        self.assertEqual(self.read("vm.yml"), "b")

    def test_pull_keeps_a_change_made_only_here_and_push_sends_it(self):
        store = FakeStore({"vm.yml": "a"})
        self.pull(store)
        self.write("vm.yml", "mine")
        self.assertEqual(self.pull(store), [])
        self.assertEqual(self.read("vm.yml"), "mine")
        self.assertEqual(self.push(store), [])
        self.assertEqual(store.files["vm.yml"], "mine")

    def test_pull_restores_a_deleted_local_file(self):
        store = FakeStore({"vm.yml": "a"})
        self.pull(store)
        (self.directory / "vm.yml").unlink()
        self.assertEqual(self.pull(store), [])
        self.assertEqual(self.read("vm.yml"), "a")

    def test_changes_on_both_sides_conflict_until_forced(self):
        store = FakeStore({"aws.tfvars": "a"})
        self.pull(store)
        store.files["aws.tfvars"] = "theirs"
        self.write("aws.tfvars", "mine")
        self.assertEqual(self.pull(store), ["aws.tfvars"])
        self.assertEqual(self.read("aws.tfvars"), "mine")
        self.assertEqual(self.push(store), ["aws.tfvars"])
        self.assertEqual(store.files["aws.tfvars"], "theirs")
        self.assertEqual(self.push(store, force=True), [])
        self.assertEqual(store.files["aws.tfvars"], "mine")

    def test_pull_force_takes_the_buckets_copy(self):
        store = FakeStore({"aws.tfvars": "a"})
        self.pull(store)
        store.files["aws.tfvars"] = "theirs"
        self.write("aws.tfvars", "mine")
        self.assertEqual(self.pull(store, force=True), [])
        self.assertEqual(self.read("aws.tfvars"), "theirs")

    def test_a_machine_that_never_synced_conflicts_on_a_different_file(self):
        self.write("secrets/postgres_app_password", "generated here")
        store = FakeStore({"secrets/postgres_app_password": "the live one"})
        self.assertEqual(self.pull(store), ["secrets/postgres_app_password"])
        self.assertEqual(self.push(store), ["secrets/postgres_app_password"])
        self.assertEqual(store.files["secrets/postgres_app_password"], "the live one")

    def test_push_leaves_a_newer_bucket_copy_alone(self):
        store = FakeStore({"lobby.env": "a"})
        self.pull(store)
        store.files["lobby.env"] = "b"
        self.assertEqual(self.push(store), [])
        self.assertEqual(store.files["lobby.env"], "b")
        self.assertEqual(store.uploads, [])

    def test_push_refuses_to_bring_back_a_file_the_bucket_lost(self):
        store = FakeStore({"smoke-session.json": "a"})
        self.pull(store)
        del store.files["smoke-session.json"]
        self.assertEqual(self.push(store), ["smoke-session.json"])
        self.assertEqual(self.push(store, force=True), [])
        self.assertEqual(store.files["smoke-session.json"], "a")

    def test_a_push_that_loses_a_race_is_a_conflict(self):
        store = FakeStore({"lobby.env": "a"})
        self.pull(store)
        self.write("lobby.env", "mine")
        listed = store.list()
        store.list = lambda: listed
        store.files["lobby.env"] = "pushed meanwhile"
        self.assertEqual(self.push(store), ["lobby.env"])
        self.assertEqual(store.files["lobby.env"], "pushed meanwhile")

    def test_new_secrets_from_a_vm_deploy_are_pushed(self):
        store = FakeStore({"secrets/postgres_app_password": "p"})
        self.pull(store)
        self.write("secrets/postgres_admin_password", "new")
        self.assertEqual(self.push(store), [])
        self.assertEqual(store.files["secrets/postgres_admin_password"], "new")

    def test_a_base_from_another_bucket_counts_for_nothing(self):
        store = FakeStore({"vm.yml": "a"})
        self.pull(store)
        self.write("vm.yml", "mine")
        other = FakeStore({"vm.yml": "other"})
        conflicts = deploy_files.pull(other, self.directory, "another-bucket", log=lambda _: None)
        self.assertEqual(conflicts, ["vm.yml"])


class RequireSecretsTest(unittest.TestCase):
    def test_a_new_deployment_may_generate_its_secrets(self):
        self.assertTrue(deploy_files.require_secrets(FakeStore(), BUCKET))

    def test_a_running_deployment_needs_its_secrets_in_the_bucket(self):
        self.assertFalse(deploy_files.require_secrets(FakeStore({"vm.yml": "a"}, state=True), BUCKET))
        self.assertTrue(deploy_files.require_secrets(FakeStore({"secrets/centrifugo_api_key": "k"}, state=True),
                                                     BUCKET))


class PrefixTest(unittest.TestCase):
    def test_everything_lies_under_mpl(self):
        # Another deployment's state, files and plans share the bucket.
        for key in (deploy_files.FILES_PREFIX, deploy_files.STATE_KEY, deploy_files.PLANS_PREFIX,
                    deploy_files.TENANTS_PREFIX):
            self.assertTrue(key.startswith("mpl/"), key)

    def test_terraform_keeps_its_state_where_the_sync_looks_for_it(self):
        versions = (pathlib.Path(deploy_files.__file__).parent / "terraform" / "versions.tf").read_text()
        self.assertIn(f'key          = "{deploy_files.STATE_KEY}"', versions)

    def test_the_bucket_must_be_named(self):
        original = os.environ.pop(deploy_files.BUCKET_ENVIRONMENT_VARIABLE, None)
        try:
            with self.assertRaisesRegex(deploy_files.AwsError, "MPL_DEPLOY_BUCKET"):
                deploy_files.bucket_name()
            os.environ[deploy_files.BUCKET_ENVIRONMENT_VARIABLE] = BUCKET
            self.assertEqual(deploy_files.bucket_name(), BUCKET)
        finally:
            os.environ.pop(deploy_files.BUCKET_ENVIRONMENT_VARIABLE, None)
            if original is not None:
                os.environ[deploy_files.BUCKET_ENVIRONMENT_VARIABLE] = original


class PublishTenantsTest(unittest.TestCase):
    def setUp(self):
        self.directory = pathlib.Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.directory)
        target = self.directory / "tenants" / "example-game" / "tenant.env"
        target.parent.mkdir(parents=True)
        target.write_text("DATABASE_PASSWORD=p\n")

    def publish(self, store):
        return deploy_files.publish_tenants(store, self.directory, log=lambda _: None)

    def test_uploads_each_tenants_settings_under_mpl_tenants(self):
        store = FakeStore()
        self.assertEqual(self.publish(store), ["mpl/tenants/example-game/tenant.env"])
        self.assertEqual(store.objects["mpl/tenants/example-game/tenant.env"][0], "DATABASE_PASSWORD=p\n")

    def test_uploads_only_what_changed(self):
        store = FakeStore()
        self.publish(store)
        self.assertEqual(self.publish(store), [])
        (self.directory / "tenants" / "example-game" / "tenant.env").write_text("DATABASE_PASSWORD=new\n")
        self.assertEqual(self.publish(store), ["mpl/tenants/example-game/tenant.env"])

    def test_without_tenants_publishes_nothing(self):
        shutil.rmtree(self.directory / "tenants")
        self.assertEqual(self.publish(FakeStore()), [])


NOW = datetime.datetime(2026, 11, 13, 12, 0, tzinfo=datetime.timezone.utc)
OLD = NOW - datetime.timedelta(days=10)


class ClaimVmTest(unittest.TestCase):
    def test_writes_the_marker_the_games_deploy_checks(self):
        store = FakeStore()
        self.assertTrue(deploy_files.claim_vm(store, log=lambda _: None))
        self.assertEqual(store.objects["mpl/vm-owner"], ("mpl\n", NOW))

    def test_is_idempotent(self):
        store = FakeStore(objects={"mpl/vm-owner": ("mpl", OLD)})
        self.assertFalse(deploy_files.claim_vm(store, log=lambda _: None))
        self.assertEqual(store.objects["mpl/vm-owner"], ("mpl", OLD))

    def test_replaces_a_marker_that_says_something_else(self):
        store = FakeStore(objects={"mpl/vm-owner": ("", OLD)})
        self.assertTrue(deploy_files.claim_vm(store, log=lambda _: None))
        self.assertEqual(store.objects["mpl/vm-owner"][0], "mpl\n")

    def test_the_marker_is_a_plain_name_in_the_deploy_roles_prefix(self):
        self.assertTrue(deploy_files.VM_OWNER_KEY.startswith("mpl/"))
        self.assertRegex(deploy_files.VM_OWNER, r"[a-z0-9][a-z0-9._-]{0,63}")


class VmScriptTest(unittest.TestCase):
    def deploy_branch(self):
        script = (pathlib.Path(deploy_files.__file__).parent.parent / "scripts" / "vm.sh").read_text()
        return script.split("  deploy)\n", 1)[1].split("    ;;", 1)[0]

    def test_the_marker_follows_the_secrets_check_and_precedes_the_playbook(self):
        branch = self.deploy_branch()
        order = [branch.index(text) for text in ("deploy_files require-secrets", "deploy_files claim-vm",
                                                 'run_playbook "$host" site.yml')]
        self.assertEqual(order, sorted(order))

    def test_a_failed_secrets_check_stops_before_the_marker(self):
        # The script runs with set -e, and the check is a plain command.
        script = (pathlib.Path(deploy_files.__file__).parent.parent / "scripts" / "vm.sh").read_text()
        self.assertIn("set -euo pipefail", script)
        self.assertNotIn("require-secrets ||", self.deploy_branch())


class ExpirePlansTest(unittest.TestCase):
    def test_deletes_only_this_repositorys_plans_older_than_the_limit(self):
        old = NOW - datetime.timedelta(days=deploy_files.SAVED_PLAN_DAYS, minutes=1)
        recent = NOW - datetime.timedelta(days=deploy_files.SAVED_PLAN_DAYS - 1)
        store = FakeStore(objects={
            "mpl/plans/1/tfplan": ("plan", old),
            "mpl/plans/1/commit": ("sha", old),
            "mpl/plans/2/tfplan": ("plan", recent),
            # Another deployment's plan, and its state: not this repository's.
            "plans/9/tfplan": ("plan", old),
            "terraform/terraform.tfstate": ("state", old),
        })
        deleted = deploy_files.expire_plans(store, NOW, log=lambda _: None)
        self.assertEqual(deleted, ["mpl/plans/1/commit", "mpl/plans/1/tfplan"])
        self.assertEqual(sorted(store.objects), ["mpl/plans/2/tfplan", "plans/9/tfplan", "terraform/terraform.tfstate"])


if __name__ == "__main__":
    unittest.main()
