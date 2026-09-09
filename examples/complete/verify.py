#!/usr/bin/env python3
"""Exercise real Terragraph commands in disposable, credential-free fixture copies."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


SOURCE = Path(__file__).resolve().parent


def require(condition, message):
    if not condition:
        raise AssertionError(message)


class Lab:
    def __init__(self, binary, tofu, name):
        self.base = Path(tempfile.mkdtemp(prefix=f"terragraph-complete-{name}-"))
        self.root = self.base / "blueprint"
        shutil.copytree(SOURCE, self.root, ignore=shutil.ignore_patterns(".terragraph", "__pycache__"))
        self.command = [binary, "--blueprint", str(self.root)] + (["--tofu"] if tofu else [])
        self.env = {
            key: value for key, value in os.environ.items()
            if not key.startswith(("AWS_", "TF_VAR_", "TF_CLI_ARGS", "TF_LOG"))
            and key not in ("TF_WORKSPACE", "TF_DATA_DIR")
        }
        self.env.update(TF_IN_AUTOMATION="1", CHECKPOINT_DISABLE="1", AWS_EC2_METADATA_DISABLED="true")
        self.index = 0
        print(f"{name}: {self.base}", flush=True)

    def run(self, *args, expect=0, payload=True, stdin=None):
        self.index += 1
        command = self.command + list(args) + (["--output", "json"] if payload else [])
        result = subprocess.run(command, env=self.env, text=True, input=stdin, capture_output=True)
        log = self.base / f"{self.index:02d}-{args[0]}.log"
        log.write_text(result.stdout + "\n" + result.stderr)
        require((result.returncode == 0) if expect == 0 else (result.returncode != 0),
                f"{' '.join(args)} exited {result.returncode}; inspect {log}")
        print(f"  {' '.join(args)}: {'ok' if expect == 0 else 'refused as expected'}", flush=True)
        if expect != 0:
            return result.stdout + result.stderr
        return json.loads(result.stdout) if payload else result.stdout

    def allow_teardown(self):
        path = self.root / "environments.hcl"
        text = path.read_text()
        require('approve = "safe"' in text, "production standing policy is missing")
        path.write_text(text.replace('approve = "safe"', 'approve = "all"'))

    def clean(self):
        shutil.rmtree(self.base)


def source_digest(root):
    return {str(path.relative_to(root)): hashlib.sha256(path.read_bytes()).hexdigest()
            for path in root.rglob("*") if path.is_file() and ".terragraph" not in path.parts
            and path.suffix in (".tf", ".hcl")}


def smoke(lab):
    digest = source_digest(lab.root)
    validation = lab.run("validate")
    require(validation == {"valid": True, "problems": []}, f"validation findings: {validation}")
    levels = lab.run("graph")["levels"]
    require(sum(map(len, levels)) == 63 and len(levels) == 12, "expected 63 leaves in 12 levels")
    first = lab.run("apply", "--auto-approve", "--parallelism", "4")
    require(len(first["nodes"]) == 63 and all(n["status"] == "applied" for n in first["nodes"]),
            "bootstrap did not apply all 63 leaves")
    second = lab.run("apply", "--auto-approve", "--parallelism", "4")
    require(all(n["status"] == "unchanged" for n in second["nodes"]), "second apply is not idempotent")
    lab.run("plan", "--parallelism", "4")
    observation = lab.run("output", "--node", "landscape")
    environments = observation["nodes"][0]["outputs"]["environments"]["value"]
    require(set(environments) == {"dev", "stg", "prd"}, "missing environment in landscape")
    require(len({value["account_id"] for value in environments.values()}) == 3, "environment accounts collide")
    require(len({value[key] for value in environments.values() for key in ("apps_vpc_id", "data_vpc_id")}) == 6,
            "environment VPC identities collide")
    require(all(len(value["services"]) == 2 for value in environments.values()), "missing application endpoints")
    require(len(list((lab.root / ".terragraph/state").glob("*.tfstate"))) == 63, "state paths are not isolated")
    observed = lab.run("output", "--node", "dev.data.database")
    secret = observed["nodes"][0]["outputs"]["credentials"]
    require(secret["redacted"] and "value" not in secret, "sensitive output was disclosed")
    snapshot = json.loads((lab.root / ".terragraph/outputs/dev.data.database.json").read_text())
    require("credentials" in snapshot["withheld"], "sensitive snapshot output was not withheld")
    require(all("fixture-only" not in p.read_text() for p in (lab.root / ".terragraph/outputs").glob("*.json")),
            "snapshot contains fake credential bytes")
    lab.run("status", "--node", "prd.platform.cluster")
    scoped = lab.run("apply", "--node", "dev.checkout.deployment", "--auto-approve")
    require([n["node"] for n in scoped["nodes"]] == ["dev.checkout.deployment"], "leaf selection expanded")
    selection_args = ("--node", "dev.checkout.deployment", "--node", "dev.payments.deployment", "--downstream")
    selected_graph = lab.run("graph", *selection_args)
    selected_names = {"dev.checkout.deployment", "dev.payments.deployment", "dev.release", "landscape"}
    require({name for level in selected_graph["levels"] for name in level} == selected_names,
            "downstream selection did not union the two application seeds")
    boundary_sources = {edge["from"]["node"] for edge in selected_graph["selection"]["boundary_edges"]}
    require({"stg.release", "prd.release"} <= boundary_sources, "selection omitted external landscape inputs")
    selected_apply = lab.run("apply", *selection_args, "--auto-approve", "--parallelism", "4")
    require({node["node"] for node in selected_apply["nodes"]} == selected_names
            and all(node["status"] == "unchanged" for node in selected_apply["nodes"]),
            "selected apply expanded into other environments or skipped selected descendants")
    require(source_digest(lab.root) == digest, "execution modified fixture source files")
    require(not list((lab.root / ".terragraph").rglob("*.tfplan")), "temporary plans were not removed")
    require(not list((lab.root / ".terragraph/vars").glob("*.json")), "temporary input files were not removed")

    # Matching type contracts do not replace actual-value checks inside the runtime.
    inputs = lab.root / "environments.hcl"
    original = inputs.read_text()
    inputs.write_text(original.replace('version = "1.34"', 'version = "1.34", private_endpoint = false'))
    lab.run("validate")
    mismatch = lab.run("plan", "--node", "prd.platform.cluster", expect=1)
    require("Production clusters require private endpoints" in mismatch, "runtime precondition did not reject public production access")
    inputs.write_text(original)

    # Contracts enforce declarations before any runtime subprocess is needed.
    contracts = lab.root / "contracts.hcl"
    original_contracts = contracts.read_text()
    marker = 'consumer "./modules/workload"'
    before, after = original_contracts.split(marker, 1)
    start, remaining = after.split('input "credentials"', 1)
    remaining = remaining.replace("sensitive = true", "sensitive = false", 1)
    contracts.write_text(before + marker + start + 'input "credentials"' + remaining)
    findings = lab.run("validate", expect=1)
    require("C005" in findings and "C008" in findings, "sensitivity mismatch did not enforce contracts")
    contracts.write_text(original_contracts)

    # A CIDR edit is a replacement even though automatic confirmation was requested.
    inputs.write_text(original.replace('10.16.0.0/16', '10.18.0.0/16'))
    refusal = lab.run("apply", "--node", "dev.network.apps", "--auto-approve", expect=1)
    require("approve" in refusal and "replace" in refusal, "safe policy did not reject VPC replacement")
    inputs.write_text(original)

    saved = lab.run("plan", "--save", "--node", "dev.checkout.deployment")
    execution = saved["executions"][0]["id"]
    lab.run("plan", "show", execution)
    lab.run("apply", "--plan", execution, "--auto-approve")
    consumed = lab.run("plan", "show", execution)
    require(consumed["executions"][0]["status"] == "completed", "saved leaf did not complete")
    lab.run("plan", "list")
    blocked = lab.run("destroy", "--auto-approve", "--parallelism", "4", expect=1)
    require("approve" in blocked, "standing production policy did not block teardown")
    lab.allow_teardown()
    destroyed = lab.run("destroy", "--auto-approve", "--parallelism", "4")
    require(len(destroyed["nodes"]) == 63 and all(n["status"] == "destroyed" for n in destroyed["nodes"]),
            "reverse-order teardown did not finish")


def saved(lab):
    record = lab.run("plan", "--save")["executions"][0]
    execution = record["id"]
    require([n["node"] for n in record["nodes"] if n.get("plan_id")] == ["organization"],
            "fresh saved plan did not stop at the ready frontier")
    rounds = 0
    while record["status"] != "completed":
        rounds += 1
        require(rounds <= 12, "saved bootstrap exceeded the graph depth")
        lab.run("plan", "show", execution)
        lab.run("apply", "--plan", execution, "--auto-approve")
        record = lab.run("plan", "show", execution)["executions"][0]
        if record["status"] != "completed":
            record = lab.run("plan", "--save", "--continue", execution)["executions"][0]
    require(rounds == 12 and all(n["phase"] == "completed" for n in record["nodes"]),
            "saved bootstrap did not complete every frontier")
    cancel_id = lab.run("plan", "--save", "--node", "organization")["executions"][0]["id"]
    lab.run("plan", "cancel", cancel_id)
    lab.run("plan", "prune")
    lab.allow_teardown()
    lab.run("destroy", "--auto-approve", "--parallelism", "4")


def native(lab):
    lab.run("apply", "--node", "organization", "--auto-approve")
    lab.run("run", "--node", "organization", "--", "init", payload=False)
    listing = lab.run("run", "--node", "organization", "--", "state", "list", payload=False)
    require("terraform_data.this" in listing, "native state list missed the fixture")
    state = json.loads(lab.run("run", "--node", "organization", "--", "state", "pull", payload=False))
    identity = state["resources"][0]["instances"][0]["attributes"]["id"]
    lab.run("run", "--node", "organization", "--", "state", "show", "terraform_data.this", payload=False)
    value = lab.run("run", "--node", "organization", "--", "console", payload=False,
                    stdin="var.organization.tenant\n")
    require('"acme"' in value, "console did not receive resolved vars")
    lab.run("run", "--node", "organization", "--", "state", "mv", "terraform_data.this", "terraform_data.renamed", payload=False)
    history = lab.run("plan", "list")["executions"]
    backup_id = next(record["id"] for record in history if record["backup_available"])
    backup = lab.run("plan", "show", backup_id, "--backup", payload=False)
    require(identity in backup, "native backup did not preserve the previous resource")
    lab.run("run", "--node", "organization", "--", "state", "mv", "terraform_data.renamed", "terraform_data.this", payload=False)
    lab.run("run", "--node", "organization", "--", "state", "rm", "terraform_data.this", payload=False)
    lab.run("run", "--node", "organization", "--", "import", "terraform_data.this", identity, payload=False)
    lab.run("apply", "--node", "organization", "--auto-approve")
    lab.run("destroy", "--node", "organization", "--auto-approve")


def vendor(lab):
    upstream = lab.base / "upstream"
    shutil.copytree(lab.root / "modules/queue", upstream / "modules/queue")
    (upstream / "modules/queue/README.md").write_text("Exclude this documentation when vendoring.\n")
    for args in (("init", "-q"), ("add", "."),
                 ("-c", "user.name=terragraph-fixture", "-c", "user.email=fixture@example.invalid",
                  "-c", "commit.gpgsign=false", "commit", "-qm", "local fixture")):
        subprocess.run(["git", "-C", str(upstream), *args], check=True, capture_output=True)
    revision = subprocess.check_output(["git", "-C", str(upstream), "rev-parse", "HEAD"], text=True).strip()
    source = "git::" + upstream.as_uri() + "//modules/queue?ref=" + revision
    for path in lab.root.glob("*.hcl"):
        path.unlink()
    group_dir = lab.root / "groups/vendor-lab"
    group_dir.mkdir()
    (group_dir / "group.hcl").write_text('''group "vendor-lab" {
  node "queue" { source = SOURCE }
  export {
    input "context" { to = node.queue.input.context }
    output "queue" { from = node.queue.output.queue }
  }
}
'''.replace("SOURCE", json.dumps(source)))
    context = {"account_id": "444444444444", "account_name": "vendor-lab", "tenant": "acme",
               "namespace": "commerce-platform", "stage": "dev", "region": "ap-northeast-2",
               "region_code": "ane2", "tags": {}}
    blueprint = 'vendor {\n  directory = "sources"\n  manifest_file = "sources.yaml"\n}\n'
    for alias, stage in (("alpha", "dev"), ("beta", "stg")):
        context["stage"] = stage
        blueprint += 'use "vendor-lab" {\n as = "' + alias + '"\n source = "./groups/vendor-lab"\n vars = {context = ' + json.dumps(context) + '}\n}\n'
    (lab.root / "blueprint.hcl").write_text(blueprint)
    (lab.root / "sources.yaml").write_text(json.dumps({"modules": [
        {"name": alias + ".queue", "source": source, "exclude": ["README.md"]}
        for alias in ("alpha", "beta")]}))
    lab.run("vendor", "--node", "alpha.queue")
    lab.run("vendor")
    lab.run("vendor", "--node", "alpha.queue", "--force")
    for alias in ("alpha", "beta"):
        package = lab.root / "sources" / (alias + ".queue")
        require((package / ".terragraph-source.json").exists(), "source subdirectory metadata is missing")
        require(not list(package.rglob("README.md")), "vendor exclusion did not remove documentation")
        require(not list(package.rglob(".git")), "vendored package includes Git internals")
    lab.run("validate")
    lab.run("apply", "--auto-approve", "--parallelism", "2")
    lab.run("destroy", "--auto-approve", "--parallelism", "2")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--terragraph", default="terragraph", help="built CLI path or command on PATH")
    parser.add_argument("--tofu", action="store_true", help="use OpenTofu instead of Terraform in a fresh copy")
    parser.add_argument("--mode", choices=("smoke", "saved", "native", "vendor", "all"), default="smoke")
    parser.add_argument("--keep", action="store_true", help="retain successful lab copies and logs")
    args = parser.parse_args()
    binary = shutil.which(args.terragraph)
    if binary is None:
        parser.error("Terragraph binary not found; run make build and pass --terragraph ./terragraph")
    for name in (("smoke", "saved", "native", "vendor") if args.mode == "all" else (args.mode,)):
        lab = Lab(str(Path(binary).resolve()), args.tofu, name)
        try:
            globals()[name](lab)
        except Exception:
            print(f"FAILED: preserved fixture and logs at {lab.base}", flush=True)
            raise
        if not args.keep:
            lab.clean()
        print(f"{name}: passed", flush=True)


if __name__ == "__main__":
    main()
