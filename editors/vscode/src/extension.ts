import * as fs from "fs";
import * as path from "path";
import * as vscode from "vscode";
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
} from "vscode-languageclient/node";

let client: LanguageClient | undefined;

export async function activate(
  context: vscode.ExtensionContext,
): Promise<void> {
  const configured = vscode.workspace
    .getConfiguration("terragraph")
    .get<string>("languageServer.path", "");
  const bundledBinary = path.join(
    context.extensionPath,
    "bin",
    process.platform === "win32" ? "terragraph.exe" : "terragraph",
  );
  const developmentBinary = path.resolve(
    context.extensionPath,
    "..",
    "..",
    "terragraph",
  );
  const command =
    configured ||
    (fs.existsSync(bundledBinary)
      ? bundledBinary
      : fs.existsSync(developmentBinary)
        ? developmentBinary
        : "terragraph");
  const output = vscode.window.createOutputChannel("Terragraph Blueprint");
  const serverOptions: ServerOptions = {
    command,
    args: ["language-server"],
  };
  const clientOptions: LanguageClientOptions = {
    documentSelector: [
      { scheme: "file", pattern: "**/blueprint.hcl" },
      { scheme: "file", pattern: "**/group.hcl" },
    ],
    outputChannel: output,
    traceOutputChannel: output,
  };

  client = new LanguageClient(
    "terragraph",
    "Terragraph Blueprint",
    serverOptions,
    clientOptions,
  );
  try {
    await client.start();
    context.subscriptions.push(client);
  } catch (error) {
    output.appendLine(`Language server failed to start: ${String(error)}`);
    client = undefined;
    void vscode.window.showErrorMessage(
      `Could not start the Terragraph language server: ${String(error)}`,
    );
  }
}

export function deactivate(): Thenable<void> | undefined {
  return client?.stop();
}
