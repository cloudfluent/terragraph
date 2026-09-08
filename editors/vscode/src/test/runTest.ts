import * as fs from "node:fs/promises";
import * as os from "node:os";
import * as path from "node:path";
import { runTests } from "@vscode/test-electron";

async function main(): Promise<void> {
  const temporary = await fs.mkdtemp(
    path.join(os.tmpdir(), "terragraph-vscode-"),
  );
  const workspace = path.join(temporary, "workspace");
  const extension = path.resolve(__dirname, "..", "..");
  try {
    await fs.mkdir(path.join(workspace, ".vscode"), { recursive: true });
    await fs.writeFile(
      path.join(workspace, ".vscode", "settings.json"),
      JSON.stringify({
        "terragraph.languageServer.path": path.resolve(
          extension,
          "..",
          "..",
          "terragraph",
        ),
      }),
    );
    await runTests({
      vscodeExecutablePath: process.env.TERRAGRAPH_VSCODE_EXECUTABLE,
      extensionDevelopmentPath: [
        extension,
        path.join(extension, "test", "fixtures", "hcl-language"),
      ],
      extensionTestsPath: path.join(__dirname, "index"),
      launchArgs: [
        workspace,
        "--disable-extensions",
        "--user-data-dir=" + path.join(temporary, "user-data"),
        "--extensions-dir=" + path.join(temporary, "extensions"),
      ],
    });
  } finally {
    await fs.rm(temporary, { recursive: true, force: true });
  }
}

main().catch((error: unknown) => {
  console.error(error);
  process.exitCode = 1;
});
