#!/usr/bin/env bash
# Deploy Postgres and Centrifugo to the VM with Ansible, dump or restore one of
# its databases, or smoke-test the deployment. See deploy/README.md.
set -euo pipefail
repository_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
ansible_directory="$repository_root/deploy/ansible"
local_directory="$repository_root/deploy/local"
settings_file="$local_directory/vm.yml"
variables_file="$local_directory/aws.tfvars"

usage() {
  cat >&2 <<'EOF'
Pulls deploy/local/ from the deploy bucket first and pushes it back at the
end, and logs in to the VM with a temporary key from Lightsail.

Usage:
  scripts/vm.sh deploy <ip> [ansible-playbook options]
      Provision the VM, or update it, a VM taken over included. Safe to
      re-run. First writes the VM owner marker mpl/vm-owner to the deploy
      bucket, which stops the game's own VM deploy. Publishes each tenant's settings to the deploy bucket under
      mpl/tenants/. The first deploy, and the first after adding a tenant,
      needs -e app_generate_secrets=true to create the missing secrets in
      deploy/local/secrets/.
  scripts/vm.sh dump <ip> [ansible-playbook options]
      Dump the lobby's database to deploy/local/dumps/, or a tenant's with
      -e app_database=<name>. With -e app_dump_lock_writes=true, first lock
      every database's role out of this VM, for a move.
  scripts/vm.sh restore <ip> <dump file> [ansible-playbook options]
      Restore a dump into the VM's empty database: the lobby's, or a
      tenant's with -e app_database=<name>.
  scripts/vm.sh smoke
      Check CloudFront, the lobby Lambda, Postgres and Centrifugo together,
      reusing the saved guest credential in deploy/local/smoke-session.json.
EOF
  exit 2
}

if [[ $# -lt 1 ]]; then
  usage
fi
command="$1"
shift

require_command() {
  if ! command -v "$1" >/dev/null; then
    echo "Install $1 first. See deploy/README.md." >&2
    exit 1
  fi
}

deploy_files() {
  python3 -I "$repository_root/deploy/deploy_files.py" "$@"
}

# Pushes what the run changed, such as new secrets or lobby.env, even when it
# failed half way, and keeps the run's own exit status.
finish() {
  local status=$?
  rm -rf "$access_directory"
  if ! deploy_files push; then
    echo "deploy/local/ isn't in the deploy bucket yet. Run scripts/deploy-files.sh push." >&2
    [[ $status -ne 0 ]] || status=1
  fi
  exit "$status"
}

require_command aws
require_command python3
deploy_files pull
# The temporary SSH keys, removed at the end.
access_directory="$(mktemp -d)"
trap finish EXIT

if [[ ! -f "$settings_file" ]]; then
  echo "Copy deploy/vm.example.yml to deploy/local/vm.yml and fill it in." >&2
  exit 1
fi

# Extra vars as JSON, so a path with spaces stays one value.
dump_file_variables() {
  python3 -c 'import json, sys; print(json.dumps({"app_dump_file": sys.argv[1]}))' "$1"
}

deploy_region() {
  local region
  if [[ -f "$variables_file" ]]; then
    region="$(sed -n -E 's/^[[:space:]]*region[[:space:]]*=[[:space:]]*"([^"]+)".*/\1/p' "$variables_file" | head -n 1)"
  fi
  region="${region:-${AWS_REGION:-${AWS_DEFAULT_REGION:-}}}"
  if [[ -z "$region" ]]; then
    echo "Set region in $variables_file, or AWS_REGION." >&2
    exit 1
  fi
  echo "$region"
}

# Asks Lightsail for a temporary SSH key and certificate for the instance whose
# public IP is host, as the browser-based SSH does, and prints the extra vars
# that make Ansible log in with them. Only AWS credentials are needed, so any
# machine can deploy. The certificate is ssh's <key>-cert.pub, which it loads
# with the key.
vm_access_variables() {
  local host="$1" region instance key
  region="$(deploy_region)"
  instance="$(aws lightsail get-instances --region "$region" \
    --query "instances[?publicIpAddress=='$host'].name | [0]" --output text)"
  if [[ -z "$instance" ]] || [[ "$instance" == None ]]; then
    echo "No Lightsail instance in $region has the public IP $host." >&2
    exit 1
  fi
  key="$access_directory/$instance"
  aws lightsail get-instance-access-details --region "$region" --instance-name "$instance" \
    --protocol ssh --output json | python3 -I -c '
import json, os, sys
details = json.load(sys.stdin)["accessDetails"]
key = sys.argv[1]
with open(os.open(key, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), "w") as file:
    file.write(details["privateKey"])
with open(key + "-cert.pub", "w") as file:
    file.write(details["certKey"] + "\n")
' "$key"
  python3 -I -c 'import json, sys; print(json.dumps({
    "ansible_ssh_private_key_file": sys.argv[1],
    "ansible_ssh_extra_args": "-o IdentitiesOnly=yes",
  }))' "$key"
}

# ansible.cfg is only read from the current directory, so playbooks run from
# deploy/ansible. Paths on the command line are resolved before that.
run_playbook() {
  local host="$1" playbook="$2" access
  shift 2
  if ! command -v ansible-playbook >/dev/null; then
    echo "Install Ansible first, e.g. 'uv tool install ansible-core'. See deploy/README.md." >&2
    exit 1
  fi
  local collections_directory="$ansible_directory/.collections/ansible_collections"
  if [[ ! -d "$collections_directory/community/docker" ]] || [[ ! -d "$collections_directory/community/crypto" ]]; then
    (cd "$ansible_directory" && ansible-galaxy collection install -r requirements.yml -p .collections)
  fi
  access="$(vm_access_variables "$host")"
  (cd "$ansible_directory" && ansible-playbook -i "$host," -e "@$settings_file" -e "$access" "$playbook" "$@")
}

case "$command" in
  deploy)
    [[ $# -ge 1 ]] || usage
    host="$1"
    shift
    deploy_files require-secrets
    # The game's own VM deploy refuses to run while this marker is there. It
    # goes first, so a run that fails half way is marked too.
    deploy_files claim-vm
    run_playbook "$host" site.yml "$@"
    # The tenants' settings, for their own deploys to read.
    deploy_files publish-tenants
    ;;
  dump)
    [[ $# -ge 1 ]] || usage
    host="$1"
    shift
    run_playbook "$host" dump.yml "$@"
    ;;
  restore)
    [[ $# -ge 2 ]] || usage
    host="$1"
    dump_file="$(realpath -- "$2")"
    shift 2
    run_playbook "$host" restore.yml -e "$(dump_file_variables "$dump_file")" "$@"
    ;;
  smoke)
    [[ $# -eq 0 ]] || usage
    lobby_url="$(sed -n -E "s/^lobby_url:[[:space:]]*[\"']?([^\"' #]+).*/\1/p" "$settings_file")"
    if [[ -z "$lobby_url" ]]; then
      echo "Set lobby_url in $settings_file. scripts/aws.sh output lobby_url prints it." >&2
      exit 1
    fi
    # The smoke test needs httpx and websockets, which the Python client's
    # locked dependencies include.
    uv run --project "$repository_root/clients/lobby-python" --locked --no-dev \
      python "$repository_root/deploy/smoke.py" \
      --lobby-url "$lobby_url" --session-file "$local_directory/smoke-session.json"
    ;;
  *)
    usage
    ;;
esac
