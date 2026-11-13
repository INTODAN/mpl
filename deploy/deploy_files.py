"""Keep deploy/local/ in step with the deploy bucket, so any machine can deploy.

The deploy bucket is a private, versioned S3 bucket in the deployment's own
AWS account. Another deployment may share it, such as the game's whose VM
this repository took over, so everything of this repository's lies under
`mpl/`, and nothing outside it is read or written here:

- `mpl/terraform/`: Terraform's state (the S3 backend writes it there).
- `mpl/plans/`: the Deploy workflow's saved plans. A plan run removes the
  ones older than SAVED_PLAN_DAYS that nobody applied.
- `mpl/files/`: every file in deploy/local/ that a deploy needs: the
  settings, the generated secrets, the Lambda's settings and the smoke
  test's guest credential. The build, plans, dumps and the tenants' settings
  stay on the machine.
- `mpl/tenants/<key>/`: what each tenant, a game with a database on the VM,
  needs from the lobby: deploy/local/tenants/<key>/, which the VM deploy
  writes and `publish-tenants` uploads. The game's own deploy reads it there.
- `mpl/vm-owner`: the VM owner marker, which `claim-vm` writes (`mpl` and a
  newline) when the VM deploy starts. The game's own VM deploy checks it and
  refuses to run while it is there. Never removed here.

scripts/aws.sh and scripts/vm.sh pull before they run and push when they end.
Each machine records in deploy/local/.sync-base.json what it last pulled or
pushed, so a sync can tell which side changed a file:

- `pull` takes the bucket's copy of a file that only the bucket changed, and
  restores a missing local file. It keeps a local change for the next push.
- `push` uploads a file that only this machine changed, or that the bucket
  doesn't have. The upload is conditional on the bucket's copy being the one
  this machine compared against, so two machines can't overwrite each other.
- A file that both sides changed since this machine last synced is a conflict.
  Nothing is overwritten unless --force says which side wins.

Nothing under mpl/files/ is ever deleted from the bucket. Its versioning keeps
every earlier copy.

Usage, from the repository root:

    python3 -I deploy/deploy_files.py status|pull|push [--force]
    python3 -I deploy/deploy_files.py bucket|has-state|require-secrets
    python3 -I deploy/deploy_files.py publish-tenants|expire-plans|claim-vm

The bucket is the one MPL_DEPLOY_BUCKET names. It exists already: the
deployment that shares it created it, or a person did (deploy/README.md).
"""

import datetime
import hashlib
import json
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile

LOCAL_DIRECTORY = pathlib.Path(__file__).resolve().parent / "local"
BASE_FILE = ".sync-base.json"
# This repository's part of the shared bucket. deploy/terraform/versions.tf
# names the same state key, and github.tf the same prefix.
PREFIX = "mpl/"
FILES_PREFIX = PREFIX + "files/"
STATE_KEY = PREFIX + "terraform/terraform.tfstate"
# The Deploy workflow's plan target saves its plan here for the apply target.
PLANS_PREFIX = PREFIX + "plans/"
SAVED_PLAN_DAYS = 3
TENANTS_DIRECTORY = "tenants"
TENANTS_PREFIX = PREFIX + "tenants/"
# The marker that tells the game's own deploy that this repository owns the VM
# (INTODAN/island-battles-go#256). It holds a plain name and a newline.
VM_OWNER_KEY = PREFIX + "vm-owner"
VM_OWNER = "mpl"
# Top-level entries of deploy/local/ that stay on the machine: Terraform's
# plans, the built Lambda, database dumps, and the tenants' settings, which
# publish-tenants uploads elsewhere.
LOCAL_ONLY = {"terraform", "lambda", "dumps", TENANTS_DIRECTORY, BASE_FILE}
BUCKET_ENVIRONMENT_VARIABLE = "MPL_DEPLOY_BUCKET"


class AwsError(Exception):
    pass


class PreconditionFailed(AwsError):
    """The bucket's copy changed between the comparison and the upload."""


def md5_of(path):
    digest = hashlib.md5(usedforsecurity=False)
    with open(path, "rb") as file:
        for block in iter(lambda: file.read(65536), b""):
            digest.update(block)
    return digest.hexdigest()


class S3Store:
    """The deploy bucket's `files/`, through the AWS CLI.

    Uploads are single-part and the bucket uses SSE-S3, so an object's ETag is
    the MD5 of its content.
    """

    def __init__(self, bucket):
        self.bucket = bucket

    def objects(self, prefix):
        """Return the bucket's objects under prefix, as list-objects-v2 lists them."""
        listing = aws_json("s3api", "list-objects-v2", "--bucket", self.bucket, "--prefix", prefix)
        return [item for item in listing.get("Contents", []) if not item["Key"].endswith("/")]

    def list(self, prefix=FILES_PREFIX):
        """Return {path relative to prefix: MD5} for every file under prefix."""
        return {item["Key"][len(prefix):]: item["ETag"].strip('"') for item in self.objects(prefix)}

    def list_dated(self, prefix):
        """Return {key: last modified, as a timezone-aware datetime} under prefix."""
        return {item["Key"]: datetime.datetime.fromisoformat(item["LastModified"].replace("Z", "+00:00"))
                for item in self.objects(prefix)}

    def put(self, key, source):
        """Upload source to key, whatever the bucket has there."""
        aws_json("s3api", "put-object", "--bucket", self.bucket, "--key", key, "--body", str(source))

    def read(self, key):
        """Return the text of the object at key, or None when the bucket has none."""
        if not any(item["Key"] == key for item in self.objects(key)):
            return None
        with tempfile.TemporaryDirectory() as directory:
            destination = pathlib.Path(directory) / "object"
            aws_json("s3api", "get-object", "--bucket", self.bucket, "--key", key, str(destination))
            return destination.read_text()

    def delete(self, key):
        aws_json("s3api", "delete-object", "--bucket", self.bucket, "--key", key)

    def download(self, path, destination):
        aws_json("s3api", "get-object", "--bucket", self.bucket, "--key", FILES_PREFIX + path, str(destination))

    def upload(self, path, source, expected):
        """Upload source as path, only while the bucket's MD5 is still expected.

        expected is None when the bucket must not have the file yet.
        """
        condition = ["--if-none-match", "*"] if expected is None else ["--if-match", f'"{expected}"']
        aws_json("s3api", "put-object", "--bucket", self.bucket, "--key", FILES_PREFIX + path,
                 "--body", str(source), *condition)

    def has_state(self):
        listing = aws_json("s3api", "list-objects-v2", "--bucket", self.bucket, "--prefix", STATE_KEY)
        return any(item["Key"] == STATE_KEY for item in listing.get("Contents", []))


def aws(*arguments):
    if shutil.which("aws") is None:
        raise AwsError("Install the AWS CLI first. See deploy/README.md.")
    result = subprocess.run(["aws", *arguments], capture_output=True, text=True)
    if result.returncode != 0:
        message = result.stderr.strip()
        if "PreconditionFailed" in message or "(412)" in message:
            raise PreconditionFailed(message)
        if "NoSuchBucket" in message:
            raise AwsError(f"{message}\nSet {BUCKET_ENVIRONMENT_VARIABLE} to the deploy bucket (deploy/README.md).")
        raise AwsError(message or f"aws {' '.join(arguments[:2])} failed")
    return result.stdout


def aws_json(*arguments):
    output = aws(*arguments, "--output", "json")
    return json.loads(output) if output.strip() else {}


def bucket_name():
    name = os.environ.get(BUCKET_ENVIRONMENT_VARIABLE)
    if not name:
        raise AwsError(f"Set {BUCKET_ENVIRONMENT_VARIABLE} to the deploy bucket, such as <name>-deploy-<account ID> "
                       "(deploy/README.md).")
    return name


def bucket_region(bucket):
    region = aws("s3api", "get-bucket-location", "--bucket", bucket, "--query", "LocationConstraint",
                 "--output", "text").strip()
    # Buckets in us-east-1 have no location constraint.
    return "us-east-1" if region in ("", "None", "null") else region


def local_files(directory, include_all=False):
    """Return {relative path: MD5} for every file in deploy/local/ that is shared,
    or, with include_all, for every file in directory."""
    files = {}
    if not directory.is_dir():
        return files
    for path in sorted(directory.rglob("*")):
        relative = path.relative_to(directory)
        if (not include_all and relative.parts[0] in LOCAL_ONLY) or path.is_symlink() or not path.is_file():
            continue
        files[relative.as_posix()] = md5_of(path)
    return files


def read_base(directory, bucket):
    try:
        base = json.loads((directory / BASE_FILE).read_text())
    except (FileNotFoundError, ValueError):
        return {}
    return base.get("files", {}) if base.get("bucket") == bucket else {}


def write_base(directory, bucket, files):
    directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    (directory / BASE_FILE).write_text(json.dumps({"bucket": bucket, "files": files}, indent=2, sort_keys=True) + "\n")


def conflict_message(paths, bucket):
    lines = ["These files changed both here and in the bucket since this machine last synced:"]
    lines += [f"  deploy/local/{path} and s3://{bucket}/{FILES_PREFIX}{path}" for path in paths]
    lines.append("Compare them, then keep one: `pull --force` takes the bucket's copies, "
                 "`push --force` sends these.")
    return "\n".join(lines)


def download_into(store, directory, path, expected):
    """Download path next to its destination, check it and move it in place."""
    destination = directory / path
    destination.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(dir=destination.parent, prefix=".download-")
    os.close(descriptor)
    try:
        store.download(path, temporary)
        if md5_of(temporary) != expected:
            raise AwsError(f"{path} changed in the bucket while it downloaded. Pull again.")
        os.chmod(temporary, 0o600)
        os.replace(temporary, destination)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def pull(store, directory, bucket, force=False, log=print):
    """Bring the bucket's changes into directory. Returns the conflicting paths."""
    remote = store.list()
    local = local_files(directory)
    base = read_base(directory, bucket)
    new_base = {path: digest for path, digest in base.items() if path in local}
    conflicts = []
    for path, remote_digest in sorted(remote.items()):
        local_digest = local.get(path)
        if local_digest == remote_digest:
            new_base[path] = remote_digest
            continue
        if local_digest is not None and base.get(path) == remote_digest:
            continue  # Only this machine changed it: the next push sends it.
        if local_digest is not None and base.get(path) != local_digest and not force:
            conflicts.append(path)
            continue
        download_into(store, directory, path, remote_digest)
        new_base[path] = remote_digest
        log(f"Pulled deploy/local/{path}")
    write_base(directory, bucket, new_base)
    return conflicts


def push(store, directory, bucket, force=False, log=print):
    """Send this machine's changes to the bucket. Returns the conflicting paths."""
    remote = store.list()
    local = local_files(directory)
    base = read_base(directory, bucket)
    new_base = {path: digest for path, digest in base.items() if path in local}
    conflicts = []
    for path, local_digest in sorted(local.items()):
        remote_digest = remote.get(path)
        if remote_digest == local_digest:
            new_base[path] = local_digest
            continue
        if remote_digest is not None and base.get(path) == local_digest:
            log(f"The bucket has a newer deploy/local/{path}: pull to get it.")
            continue
        # A file the bucket lost, or that changed there too, needs --force.
        if base.get(path) != remote_digest and not force:
            conflicts.append(path)
            continue
        try:
            store.upload(path, directory / path, remote_digest)
        except PreconditionFailed:
            conflicts.append(path)
            continue
        new_base[path] = local_digest
        log(f"Pushed deploy/local/{path}")
    write_base(directory, bucket, new_base)
    return conflicts


def status(store, directory, bucket, log=print):
    remote = store.list()
    local = local_files(directory)
    base = read_base(directory, bucket)
    for path in sorted(set(remote) | set(local)):
        local_digest, remote_digest = local.get(path), remote.get(path)
        if local_digest == remote_digest:
            continue
        if local_digest is None:
            state = "only in the bucket"
        elif remote_digest is None:
            state = "only here"
        elif base.get(path) == remote_digest:
            state = "changed here"
        elif base.get(path) == local_digest:
            state = "changed in the bucket"
        else:
            state = "changed on both sides"
        log(f"{state}: {path}")


def require_secrets(store, bucket):
    """Refuse a VM deploy that would generate a running deployment's secrets again.

    Once Terraform's state is in the bucket, a deployment exists, and its
    secrets must be too. Without them the VM deploy would stop, or with
    app_generate_secrets=true replace every password and key.
    """
    if any(path.startswith("secrets/") for path in store.list()):
        return True
    return not store.has_state()


def publish_tenants(store, directory, log=print):
    """Upload deploy/local/tenants/ to mpl/tenants/: each file that differs.

    The VM deploy writes the files from the secrets every time, so this
    machine's copy is the current one. Returns the keys it uploaded.
    """
    local = local_files(directory / TENANTS_DIRECTORY, include_all=True)
    remote = store.list(TENANTS_PREFIX)
    uploaded = []
    for path, digest in sorted(local.items()):
        if remote.get(path) == digest:
            continue
        key = TENANTS_PREFIX + path
        store.put(key, directory / TENANTS_DIRECTORY / path)
        uploaded.append(key)
        log(f"Published deploy/local/{TENANTS_DIRECTORY}/{path} to {key}")
    return uploaded


def claim_vm(store, log=print):
    """Write the VM owner marker, unless it already says this repository owns the VM.

    Returns whether it wrote it. The game's own VM deploy refuses to run while
    the marker is there, so the VM deploy writes it before it changes anything
    on the VM: a first run that fails half way is marked too. Nothing here
    removes it.
    """
    if (store.read(VM_OWNER_KEY) or "").strip() == VM_OWNER:
        return False
    with tempfile.TemporaryDirectory() as directory:
        source = pathlib.Path(directory) / "vm-owner"
        source.write_text(VM_OWNER + "\n")
        store.put(VM_OWNER_KEY, source)
    log(f"Wrote the VM owner marker {VM_OWNER_KEY}: {VM_OWNER}")
    return True


def expire_plans(store, now, log=print):
    """Delete saved plans older than SAVED_PLAN_DAYS, which nobody applied.

    They hold the Lambda's settings. The shared bucket's own lifecycle rules
    belong to the deployment that created it, so this repository's plans are
    expired here. Returns the keys it deleted.
    """
    deleted = []
    for key, modified in sorted(store.list_dated(PLANS_PREFIX).items()):
        if now - modified > datetime.timedelta(days=SAVED_PLAN_DAYS):
            store.delete(key)
            deleted.append(key)
            log(f"Deleted the unapplied plan {key}")
    return deleted


def main(arguments):
    commands = ("status", "pull", "push", "bucket", "has-state", "require-secrets", "publish-tenants",
                "expire-plans", "claim-vm")
    if not arguments or arguments[0] not in commands:
        print(f"Usage: deploy_files.py {'|'.join(commands)} [--force]", file=sys.stderr)
        return 2
    command, options = arguments[0], arguments[1:]
    force = "--force" in options

    # stdout is only for `bucket` and `has-state`, which scripts/aws.sh reads.
    def log(message):
        print(message, file=sys.stderr)

    try:
        bucket = bucket_name()
        if command == "bucket":
            print(bucket, bucket_region(bucket))
            return 0
        store = S3Store(bucket)
        if command == "has-state":
            print("yes" if store.has_state() else "no")
            return 0
        if command == "require-secrets":
            if require_secrets(store, bucket):
                return 0
            log(f"s3://{bucket}/{PREFIX} has the deployment's Terraform state but no secrets. Push deploy/local/ "
                "from the machine that has them (scripts/deploy-files.sh push), or copy the secrets of the VM "
                "taken over (deploy/README.md), before deploying the VM.")
            return 1
        if command == "publish-tenants":
            publish_tenants(store, LOCAL_DIRECTORY, log)
            return 0
        if command == "claim-vm":
            claim_vm(store, log)
            return 0
        if command == "expire-plans":
            expire_plans(store, datetime.datetime.now(datetime.timezone.utc), log)
            return 0
        if command == "status":
            status(store, LOCAL_DIRECTORY, bucket, log)
            return 0
        sync = pull if command == "pull" else push
        conflicts = sync(store, LOCAL_DIRECTORY, bucket, force, log)
    except AwsError as error:
        log(f"The deploy bucket: {error}")
        return 1
    if conflicts:
        log(conflict_message(conflicts, bucket))
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
