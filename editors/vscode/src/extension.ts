import * as fs from "fs";
import * as path from "path";
import * as vscode from "vscode";
import { LanguageClient, LanguageClientOptions, ServerOptions } from "vscode-languageclient/node";

let client: LanguageClient | undefined;

export async function activate(context: vscode.ExtensionContext): Promise<void> {
  const configured = vscode.workspace.getConfiguration("terragraph").get<string>("languageServer.path", "");
  const developmentBinary = path.resolve(context.extensionPath, "..", "..", "terragraph");
  const command = configured || (fs.existsSync(developmentBinary) ? developmentBinary : "terragraph");
  const output = vscode.window.createOutputChannel("Terragraph Blueprint");
  const serverOptions: ServerOptions = {
    command,
    args: ["language-server"],
  };
  const clientOptions: LanguageClientOptions = {
    documentSelector: [
      { scheme: "file", pattern: "**/blueprint.hcl" },
    ],
    outputChannel: output,
    traceOutputChannel: output,
  };

  client = new LanguageClient("terragraph", "Terragraph Blueprint", serverOptions, clientOptions);
  try {
    await client.start();
    context.subscriptions.push(client);
  } catch (error) {
    output.appendLine(`언어 서버 시작 실패: ${String(error)}`);
    client = undefined;
    void vscode.window.showErrorMessage(`Terragraph language server를 시작하지 못했습니다: ${String(error)}`);
  }
}

export function deactivate(): Thenable<void> | undefined {
  return client?.stop();
}
