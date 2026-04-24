// Connect to /bind-vscode and expose vscode.* methods to the webui via JSON-RPC 2.0.

import WebSocket from "ws";
import * as vscode from "vscode";
import { JsonRpcRequest, JsonRpcResponse } from "./types";

type Handler = (params: any) => Promise<unknown> | unknown;

const methods: Record<string, Handler> = {
  "vscode.showMessage": async (p: any) => {
    const level = String(p?.level ?? "info");
    const text = String(p?.text ?? "");
    if (level === "error") await vscode.window.showErrorMessage(text);
    else if (level === "warn") await vscode.window.showWarningMessage(text);
    else await vscode.window.showInformationMessage(text);
    return true;
  },
  "vscode.openFile": async (p: any) => {
    const fp = String(p?.path ?? "");
    if (!fp) throw new Error("path required");
    const doc = await vscode.workspace.openTextDocument(vscode.Uri.file(fp));
    const opts: vscode.TextDocumentShowOptions = {};
    if (typeof p?.line === "number") {
      const line = Math.max(0, p.line - 1);
      opts.selection = new vscode.Range(line, 0, line, 0);
    }
    await vscode.window.showTextDocument(doc, opts);
    return true;
  },
  "vscode.showQuickPick": async (p: any) => {
    const items: string[] = Array.isArray(p?.items)
      ? p.items.map((x: unknown) => String(x))
      : [];
    return (
      (await vscode.window.showQuickPick(items, {
        placeHolder: p?.placeholder ? String(p.placeholder) : undefined,
      })) ?? null
    );
  },
  "vscode.copyToClipboard": async (p: any) => {
    await vscode.env.clipboard.writeText(String(p?.text ?? ""));
    return true;
  },
  "vscode.openExternal": async (p: any) => {
    await vscode.env.openExternal(vscode.Uri.parse(String(p?.url ?? "")));
    return true;
  },
};

/**
 * BindConnection is a single WebSocket connection to the webui's /bind-vscode.
 * Incoming JSON-RPC requests invoke the corresponding `vscode.*` method.
 */
export class BindConnection {
  private ws: WebSocket;
  private closed = false;

  constructor(
    wsUrl: string,
    token: string,
    output: vscode.OutputChannel,
  ) {
    const full =
      wsUrl + (wsUrl.includes("?") ? "&" : "?") + "token=" + encodeURIComponent(token);
    this.ws = new WebSocket(full);
    this.ws.on("open", () => output.appendLine(`bind: connected ${wsUrl}`));
    this.ws.on("message", (data) => this.onMessage(data.toString("utf8")));
    this.ws.on("close", () => {
      if (!this.closed) output.appendLine("bind: connection closed");
    });
    this.ws.on("error", (err) => output.appendLine("bind: error " + err.message));
  }

  async close(): Promise<void> {
    this.closed = true;
    try {
      this.ws.close();
    } catch {
      // ignore
    }
  }

  private async onMessage(text: string): Promise<void> {
    let msg: JsonRpcRequest;
    try {
      msg = JSON.parse(text);
    } catch {
      return;
    }
    if (typeof msg.method !== "string" || msg.jsonrpc !== "2.0") return;
    const handler = methods[msg.method];
    const id = msg.id ?? null;
    try {
      if (!handler) {
        this.send({
          jsonrpc: "2.0",
          id,
          error: { code: -32601, message: "method not found: " + msg.method },
        });
        return;
      }
      const result = await handler(msg.params);
      this.send({ jsonrpc: "2.0", id, result });
    } catch (err: any) {
      this.send({
        jsonrpc: "2.0",
        id,
        error: { code: -32000, message: err?.message ?? String(err) },
      });
    }
  }

  private send(resp: JsonRpcResponse): void {
    try {
      this.ws.send(JSON.stringify(resp));
    } catch {
      // socket may be closed.
    }
  }
}
