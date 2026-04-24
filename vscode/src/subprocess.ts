// Spawn `md webui` subprocesses and parse their stdout start line.
// Reuses one subprocess per file path; refcounted across panels.

import { spawn, ChildProcess } from "node:child_process";
import * as path from "node:path";
import * as vscode from "vscode";
import * as readline from "node:readline";
import { StartInfo } from "./types";

interface Entry {
  child: ChildProcess;
  info: Promise<StartInfo>;
  refs: number;
}

const entries = new Map<string, Entry>();

/**
 * Start (or reuse) an md webui subprocess for the given TASKS file path.
 * Each caller must balance start() with stop().
 */
export function start(
  mdBin: string,
  filePath: string,
  output: vscode.OutputChannel,
): Promise<StartInfo> {
  const key = path.normalize(filePath);
  const existing = entries.get(key);
  if (existing) {
    existing.refs += 1;
    return existing.info;
  }

  const cwd = path.dirname(filePath);
  const child = spawn(
    mdBin,
    ["--tasks-file", filePath, "webui", "--port=0", "--print=json"],
    {
      cwd,
      env: { ...process.env, MD_TASKS: filePath },
      stdio: ["ignore", "pipe", "pipe"],
    },
  );
  output.appendLine(
    `spawn: ${mdBin} webui for ${filePath} (pid=${child.pid})`,
  );

  const info = new Promise<StartInfo>((resolve, reject) => {
    const rl = readline.createInterface({ input: child.stdout! });
    const onTimeout = setTimeout(() => {
      rl.close();
      reject(new Error("md webui did not print MEADS_WEBUI line within 5s"));
    }, 5000);
    rl.on("line", (line) => {
      if (!line.startsWith("MEADS_WEBUI ")) {
        output.appendLine(`[stdout] ${line}`);
        return;
      }
      clearTimeout(onTimeout);
      rl.close();
      try {
        const json = JSON.parse(line.slice("MEADS_WEBUI ".length)) as StartInfo;
        resolve(json);
      } catch (err) {
        reject(err);
      }
    });
    child.on("error", (err) => {
      clearTimeout(onTimeout);
      reject(err);
    });
    child.on("exit", (code) => {
      clearTimeout(onTimeout);
      reject(new Error(`md webui exited early (code=${code})`));
    });
  });

  child.stderr?.on("data", (buf: Buffer) => {
    for (const line of buf.toString("utf8").split(/\r?\n/)) {
      if (line) output.appendLine(`[stderr] ${line}`);
    }
  });
  child.on("exit", (code, signal) => {
    output.appendLine(`exit: ${filePath} (code=${code}, signal=${signal})`);
    entries.delete(key);
  });

  entries.set(key, { child, info, refs: 1 });
  return info;
}

/** Decrement refcount; kill subprocess when it reaches zero. */
export function stop(filePath: string): void {
  const key = path.normalize(filePath);
  const e = entries.get(key);
  if (!e) return;
  e.refs -= 1;
  if (e.refs > 0) return;
  entries.delete(key);
  try {
    e.child.kill("SIGTERM");
  } catch {
    // ignore
  }
  // Escalate to SIGKILL after 2s if still alive.
  const pid = e.child.pid;
  if (pid === undefined) return;
  setTimeout(() => {
    try {
      process.kill(pid, 0);
      e.child.kill("SIGKILL");
    } catch {
      // Already exited.
    }
  }, 2000);
}

/** Kill every running subprocess. Called from the extension's deactivate(). */
export function stopAll(): void {
  for (const [key, e] of entries) {
    try {
      e.child.kill("SIGTERM");
    } catch {
      // ignore
    }
    entries.delete(key);
  }
}
