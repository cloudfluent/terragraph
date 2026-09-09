import * as assert from "node:assert/strict";
import * as fs from "node:fs/promises";
import * as path from "node:path";
import { setTimeout } from "node:timers/promises";
import * as vscode from "vscode";

// Provider registration and diagnostics cross the extension-host/LSP boundary asynchronously.
async function eventually(
  description: string,
  check: () => Promise<boolean> | boolean,
): Promise<void> {
  const deadline = Date.now() + 10_000;
  while (Date.now() < deadline) {
    if (await check()) return;
    await setTimeout(50);
  }
  assert.fail(description);
}

async function replace(
  document: vscode.TextDocument,
  text: string,
): Promise<void> {
  const edit = new vscode.WorkspaceEdit();
  edit.replace(
    document.uri,
    new vscode.Range(
      document.positionAt(0),
      document.positionAt(document.getText().length),
    ),
    text,
  );
  assert.equal(await vscode.workspace.applyEdit(edit), true);
}

async function checkEditingFeatures(
  root: string,
  filename: string,
  group: boolean,
): Promise<void> {
  const dir = path.join(root, filename.replace(".hcl", ""));
  await fs.mkdir(path.join(dir, "module"), { recursive: true });
  await fs.writeFile(
    path.join(dir, "module", "main.tf"),
    'variable "vpc_id" { type = string }\noutput "vpc_id" {\n  value = "vpc-123"\n  description = "Network identifier"\n}\n',
  );
  const uri = vscode.Uri.file(path.join(dir, filename));
  const body =
    'node "vpc" { source = "./module" }\nedge {\n  from = node.vpc.output.v\n  to = node.vpc.input.vpc_id\n}\n';
  const initial = group ? 'group "network" {\n' + body + "}\n" : body;
  await fs.writeFile(uri.fsPath, initial);
  const document = await vscode.workspace.openTextDocument(uri);
  await vscode.window.showTextDocument(document);
  assert.equal(document.languageId, "hcl");
  const outputEnd =
    initial.indexOf("node.vpc.output.v") + "node.vpc.output.v".length;
  await eventually(`${filename}: missing vpc_id completion`, async () => {
    const completions =
      await vscode.commands.executeCommand<vscode.CompletionList>(
        "vscode.executeCompletionItemProvider",
        uri,
        document.positionAt(outputEnd),
      );
    return (
      completions?.items.some(
        (item) =>
          item.label === "vpc_id" &&
          (typeof item.documentation === "string"
            ? item.documentation
            : item.documentation?.value
          )?.includes("Network identifier"),
      ) ?? false
    );
  });

  const complete = initial.replace(
    "node.vpc.output.v",
    "node.vpc.output.vpc_id",
  );
  await replace(document, complete);
  assert.equal(document.isDirty, true);
  const definitions = await vscode.commands.executeCommand<vscode.Location[]>(
    "vscode.executeDefinitionProvider",
    uri,
    document.positionAt(complete.indexOf("node.vpc.output") + "node.".length),
  );
  assert.equal(
    definitions?.[0]?.uri.toString(),
    uri.toString(),
    `${filename}: definition`,
  );
  assert.equal(definitions[0].range.start.line, group ? 1 : 0);

  await replace(document, complete.replace("output.vpc_id", "output.missing"));
  await eventually(`${filename}: missing diagnostic after unsaved edit`, () =>
    vscode.languages
      .getDiagnostics(uri)
      .some(
        (diagnostic) =>
          diagnostic.source === "terragraph" &&
          diagnostic.message.includes("missing"),
      ),
  );
  await replace(document, complete);
  await eventually(
    `${filename}: stale diagnostic after repair`,
    () => vscode.languages.getDiagnostics(uri).length === 0,
  );
  assert.equal(await fs.readFile(uri.fsPath, "utf8"), initial);
  console.log(
    `PASS ${filename}: completion metadata, definition, unsaved diagnostics`,
  );
}

async function checkContractEditing(root: string): Promise<void> {
  const dir = path.join(root, "native-contracts");
  await fs.mkdir(path.join(dir, "module"), { recursive: true });
  await fs.writeFile(
    path.join(dir, "module", "main.tf"),
    'variable "value" { type = list(string) }\n',
  );
  const uri = vscode.Uri.file(path.join(dir, "contracts.hcl"));
  const initial =
    'consumer "./module" {\n input "value" {\n  type = li\n }\n}\n';
  await fs.writeFile(uri.fsPath, initial);
  const document = await vscode.workspace.openTextDocument(uri);
  await vscode.window.showTextDocument(document);
  await eventually("native contract completion", async () => {
    const result = await vscode.commands.executeCommand<vscode.CompletionList>(
      "vscode.executeCompletionItemProvider",
      uri,
      document.positionAt(initial.indexOf("li\n") + 2),
    );
    return result?.items.some((item) => item.label === "list") ?? false;
  });
  const valid = initial.replace("type = li", "type = list(string)");
  await replace(document, valid);
  await eventually("native contract hover", async () => {
    const hovers = await vscode.commands.executeCommand<vscode.Hover[]>(
      "vscode.executeHoverProvider",
      uri,
      document.positionAt(valid.indexOf("type =")),
    );
    return (
      hovers?.some((hover) =>
        hover.contents.some((content) =>
          typeof content === "string"
            ? content.includes("list(string)")
            : content.value.includes("list(string)"),
        ),
      ) ?? false
    );
  });
  await replace(document, valid.replace("list(string)", "list(invalid_type)"));
  await eventually("native contract type diagnostic", () =>
    vscode.languages
      .getDiagnostics(uri)
      .some(
        (diagnostic) =>
          diagnostic.source === "terragraph" &&
          diagnostic.message.includes("type"),
      ),
  );
  await replace(document, valid);
  await eventually(
    "native contract repaired",
    () => vscode.languages.getDiagnostics(uri).length === 0,
  );
  console.log(
    "PASS native contracts: type completion, inherited type hover, unsaved type diagnostics",
  );
}

async function checkBackendAddressEditing(root: string): Promise<void> {
  const dir = path.join(root, "backend-address");
  await fs.mkdir(path.join(dir, "module"), { recursive: true });
  await fs.writeFile(
    path.join(dir, "module", "main.tf"),
    'output "id" { value = "x" }\n',
  );
  const uri = vscode.Uri.file(path.join(dir, "blueprint.hcl"));
  const initial =
    'node "app" {\n source = "./module"\n backend_address = {\n  s3_key_\n }\n}\n';
  await fs.writeFile(uri.fsPath, initial);
  const document = await vscode.workspace.openTextDocument(uri);
  await vscode.window.showTextDocument(document);
  await eventually("backend address prefix and name completions", async () => {
    const completions =
      await vscode.commands.executeCommand<vscode.CompletionList>(
        "vscode.executeCompletionItemProvider",
        uri,
        document.positionAt(initial.indexOf("s3_key_") + "s3_key_".length),
      );
    return ["s3_key_prefix", "s3_key_name"].every((name) =>
      completions?.items.some((item) => item.label === name),
    );
  });
  const invalid = initial.replace(
    "s3_key_",
    's3_key_name = "../shared.tfstate"',
  );
  await replace(document, invalid);
  await eventually("backend address file name diagnostic", () =>
    vscode.languages
      .getDiagnostics(uri)
      .some(
        (diagnostic) =>
          diagnostic.source === "terragraph" &&
          diagnostic.message.includes("backend_address.s3_key_name"),
      ),
  );
  await replace(document, invalid.replace("../shared.tfstate", "state.json"));
  await eventually(
    "backend address repaired",
    () => vscode.languages.getDiagnostics(uri).length === 0,
  );
  console.log(
    "PASS backend address: prefix and name completion, unsaved diagnostics and repair",
  );
}

export async function run(): Promise<void> {
  const root = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
  assert.ok(root, "test workspace is required");
  await checkContractEditing(root);
  await checkBackendAddressEditing(root);
  for (const [filename, group] of [
    ["blueprint.hcl", false],
    ["group.hcl", true],
    ["nodes.hcl", false],
    ["edges.hcl", false],
    ["contracts.hcl", false],
    ["components.hcl", true],
  ] as const) {
    await checkEditingFeatures(root, filename, group);
  }
}
