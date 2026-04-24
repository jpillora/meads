// Locate the `md` binary. If it can't be found, prompt the user to install.

import { execFile } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import * as vscode from "vscode";

const exe = process.platform === "win32" ? "md.exe" : "md";

/**
 * Resolve a usable md binary path. Throws if not found and the user declines
 * to install.
 */
export async function resolveMd(
  output: vscode.OutputChannel,
): Promise<string> {
  // 1. Explicit override.
  const override = vscode.workspace
    .getConfiguration("meads")
    .get<string>("mdPath");
  if (override) {
    if (await isExecutable(override)) return override;
    void vscode.window.showWarningMessage(
      `meads.mdPath=${override} is not executable; falling back to auto-detect.`,
    );
  }

  // 2. PATH lookup.
  const onPath = await whichMd();
  if (onPath) return onPath;

  // 3. Common install dirs.
  const candidates = buildCandidates();
  for (const c of candidates) {
    if (await isExecutable(c)) return c;
  }

  // 4. Prompt.
  return promptInstall(output);
}

async function whichMd(): Promise<string | null> {
  const cmd = process.platform === "win32" ? "where" : "which";
  return new Promise((resolve) => {
    execFile(cmd, [exe], (err, stdout) => {
      if (err) return resolve(null);
      const line = stdout.split(/\r?\n/).find((l) => l.trim().length > 0);
      resolve(line ? line.trim() : null);
    });
  });
}

function buildCandidates(): string[] {
  const home = os.homedir();
  const env = process.env;
  const list: string[] = [];
  if (env.GOPATH) list.push(path.join(env.GOPATH, "bin", exe));
  list.push(path.join(home, "go", "bin", exe));
  list.push(path.join(home, ".local", "bin", exe));
  list.push(path.join("/usr", "local", "bin", exe));
  list.push(path.join("/opt", "homebrew", "bin", exe));
  return list;
}

async function isExecutable(p: string): Promise<boolean> {
  try {
    await fs.promises.access(p, fs.constants.X_OK);
    return true;
  } catch {
    return false;
  }
}

async function promptInstall(
  output: vscode.OutputChannel,
): Promise<string> {
  const choice = await vscode.window.showErrorMessage(
    "`md` (meads) binary not found. Install it to use the meads extension.",
    { modal: false },
    "Install via go install",
    "Download release",
    "Set path…",
  );
  if (choice === "Install via go install") {
    const term = vscode.window.createTerminal("meads: install");
    term.show(true);
    term.sendText(
      "go install github.com/jpillora/meads/cmd/md@latest && echo 'meads installed — reopen TASKS.md'",
    );
    throw new Error(
      "meads: install started in terminal. Reopen TASKS.md once it completes.",
    );
  }
  if (choice === "Download release") {
    await vscode.env.openExternal(
      vscode.Uri.parse("https://github.com/jpillora/meads/releases/latest"),
    );
    throw new Error(
      "meads: opened releases page. Install, then reopen TASKS.md.",
    );
  }
  if (choice === "Set path…") {
    await vscode.commands.executeCommand(
      "workbench.action.openSettings",
      "meads.mdPath",
    );
    throw new Error("meads: configure meads.mdPath and reopen TASKS.md.");
  }
  output.appendLine("md not found and user declined install");
  throw new Error("meads: `md` binary not found");
}

/**
 * Best-effort version check. Logs the version but does not block on mismatch.
 */
export async function getVersion(bin: string): Promise<string | null> {
  return new Promise((resolve) => {
    execFile(bin, ["--version"], (err, stdout) => {
      if (err) return resolve(null);
      const m = stdout.trim().match(/\d+\.\d+\.\d+\S*/);
      resolve(m ? m[0] : null);
    });
  });
}
