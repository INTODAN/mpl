"""Inspect the Terraform plan that scripts/aws.sh is about to apply.

scripts/aws.sh pipes `terraform show -json <plan>` into this script, with one
of four modes:

`deploy` refuses a plan that a lobby deploy must not apply. `scripts/aws.sh
deploy` applies its plan without asking, so it allows only creating or
updating resources other than Lightsail's, ACM's and CloudFront's, and no
import. A deploy then never touches the VM, its firewall or its static IP, or
the lobby's certificate and distribution, and never deletes or replaces
anything, such as the Function URL that CloudFront calls. Creating the
certificate would wait for a DNS record that only a person can add. Anything
else goes through `scripts/aws.sh apply`, which shows the plan and asks.

`imports` refuses a plan that imports anything with a change, or that
changes a Lightsail resource in the same plan as it imports. Taking over a
running VM (deploy/README.md, "Taking over a running VM") must import its
instances, its static IP and the attachment exactly as they run: 0 to change
and 0 to destroy. A change there would reach a VM that serves players, so
the takeover's settings are fixed until the plan imports with no change.
scripts/aws.sh runs it before every apply and when it saves a plan.

`apply` prints, one per line, the name of every Lightsail instance, static IP
or disk, and every CloudFront distribution, the plan deletes or replaces, and
describes them on stderr. `scripts/aws.sh apply` then asks for each name to be
typed, because deleting an instance deletes its disk and its databases, and a
new distribution has a new domain for the lobby's CNAME.

`publishes-lobby` prints `yes` when the plan creates or updates the lobby
function, so `scripts/aws.sh apply` runs the lobby's migrations first, and
`no` otherwise. An apply that only moves the static IP, for example, must not
migrate.
"""

import json
import sys

ALLOWED_ACTIONS = (["create"], ["update"])
LOBBY_FUNCTION = "aws_lambda_function.lobby"
LIGHTSAIL_PREFIX = "aws_lightsail_"
# A deploy changes none of these: the VM and the lobby's name.
REFUSED_TYPE_PREFIXES = {
    LIGHTSAIL_PREFIX: "a Lightsail resource",
    "aws_acm_": "the lobby's certificate",
    "aws_cloudfront_": "the lobby's distribution",
}
# Deleting one of these loses data, or an address that DNS points at.
DESTRUCTIVE_TYPES = {
    "aws_lightsail_instance": "instance, with its disk and its databases",
    "aws_lightsail_static_ip": "static IP, which vm.<domain> points at",
    "aws_lightsail_disk": "disk",
    "aws_cloudfront_distribution": "distribution, whose domain the lobby's CNAME points at",
}
MODES = ("deploy", "imports", "apply", "publishes-lobby")


def resource_changes(plan):
    """Yield every managed resource change in the plan, no-ops included."""
    for change in plan.get("resource_changes", []):
        if change.get("mode") == "managed":
            yield change


def managed_changes(plan):
    """Yield every managed resource change in the plan that does something."""
    for change in resource_changes(plan):
        if change["change"]["actions"] != ["no-op"]:
            yield change


def importing(change):
    """Whether the change imports an existing object into the state."""
    return change["change"].get("importing") is not None


def describe(change):
    return f"{change['address']}: {'/'.join(change['change']['actions'])}"


def imported(plan):
    """Return the address of every resource the plan imports."""
    return [change["address"] for change in resource_changes(plan) if importing(change)]


def refused_changes(plan):
    """Return a description of every change in the plan a deploy may not make."""
    refused = []
    for change in resource_changes(plan):
        if importing(change):
            refused.append(f"{change['address']}: import")
    for change in managed_changes(plan):
        actions = change["change"]["actions"]
        kind = next((kind for prefix, kind in REFUSED_TYPE_PREFIXES.items() if change["type"].startswith(prefix)), None)
        if kind is not None:
            refused.append(f"{describe(change)} ({kind})")
        elif actions not in ALLOWED_ACTIONS:
            refused.append(describe(change))
    return refused


def refused_imports(plan):
    """Return a description of every import that changes something, and of
    every Lightsail change in a plan that imports."""
    refused = []
    imports = False
    for change in resource_changes(plan):
        if not importing(change):
            continue
        imports = True
        if change["change"]["actions"] != ["no-op"]:
            refused.append(f"{describe(change)} (an import must change nothing)")
    if not imports:
        return refused
    for change in managed_changes(plan):
        if change["type"].startswith(LIGHTSAIL_PREFIX) and not importing(change):
            refused.append(f"{describe(change)} (a Lightsail resource, in a plan that takes over a VM)")
    return refused


def destroyed_resources(plan):
    """Return (name, description) for every destructive deletion or replacement."""
    destroyed = []
    for change in managed_changes(plan):
        kind = DESTRUCTIVE_TYPES.get(change["type"])
        actions = change["change"]["actions"]
        if kind is None or "delete" not in actions:
            continue
        before = change["change"].get("before") or {}
        # A distribution has no name; its domain is what the CNAME holds.
        name = before.get("name") or before.get("domain_name") or change["address"]
        verb = "deletes" if actions == ["delete"] else "replaces"
        destroyed.append((name, f"{change['address']}: {verb} the {kind}"))
    return destroyed


def publishes_lobby(plan):
    """Whether the plan creates the lobby function or changes it."""
    return any(change["address"] == LOBBY_FUNCTION for change in managed_changes(plan))


def main(arguments):
    if len(arguments) != 1 or arguments[0] not in MODES:
        print(f"Usage: terraform show -json <plan> | check_deploy_plan.py {'|'.join(MODES)}", file=sys.stderr)
        return 2
    mode = arguments[0]
    plan = json.load(sys.stdin)
    if mode == "publishes-lobby":
        print("yes" if publishes_lobby(plan) else "no")
        return 0
    if mode == "apply":
        destroyed = destroyed_resources(plan)
        if destroyed:
            print("This plan destroys resources that are hard to get back:", file=sys.stderr)
            for name, description in destroyed:
                print(f"  {description}", file=sys.stderr)
                print(name)
        return 0
    if mode == "imports":
        refused = refused_imports(plan)
        addresses = imported(plan)
        if not refused:
            if addresses:
                print(f"The plan imports {len(addresses)} resources, each with no change:", file=sys.stderr)
                for address in addresses:
                    print(f"  {address}", file=sys.stderr)
            return 0
        print("This plan would change what it imports, so it isn't applied:", file=sys.stderr)
        for line in refused:
            print(f"  {line}", file=sys.stderr)
        print("Make aws.tfvars describe the running VM exactly as it is, with manage_vm_firewall = false "
              "while another configuration manages its firewall, and plan again (deploy/README.md, "
              "\"Taking over a running VM\").", file=sys.stderr)
        return 1
    refused = refused_changes(plan)
    if not refused:
        return 0
    print("This plan changes more than the lobby, so the deploy stops before migrating:", file=sys.stderr)
    for line in refused:
        print(f"  {line}", file=sys.stderr)
    print("Review and apply these changes with scripts/aws.sh apply, then deploy again.", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
