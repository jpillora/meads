// CustomTextEditorProvider that spawns `md webui` for TASKS files
// and renders its UI inside a VS Code webview.

import * as vscode from "vscode";
import { BindConnection } from "./bindClient";
import { resolveMd } from "./mdBinary";
import { start, stop } from "./subprocess";
import { webviewHTML } from "./webview";

export class MeadsEditorProvider implements vscode.CustomTextEditorProvider {
  public static readonly viewType = "meads.taskEditor";

  constructor(private output: vscode.OutputChannel) {}

  async resolveCustomTextEditor(
    document: vscode.TextDocument,
    webviewPanel: vscode.WebviewPanel,
    _token: vscode.CancellationToken,
  ): Promise<void> {
    const filePath = document.uri.fsPath;
    webviewPanel.webview.options = {
      enableScripts: true,
      localResourceRoots: [],
    };
    webviewPanel.webview.html = placeholderHTML("Starting meads…");

    let mdBin: string;
    try {
      mdBin = await resolveMd(this.output);
    } catch (err: any) {
      webviewPanel.webview.html = placeholderHTML(
        `<strong>meads:</strong> ${escapeHtml(err.message)}`,
      );
      return;
    }

    let info;
    try {
      info = await start(mdBin, filePath, this.output);
    } catch (err: any) {
      webviewPanel.webview.html = placeholderHTML(
        `<strong>meads:</strong> failed to start: ${escapeHtml(err.message)}`,
      );
      return;
    }

    webviewPanel.webview.html = webviewHTML(info.url, info.token);

    // Bind a reverse-RPC channel from the extension host.
    const wsURL = info.url.replace(/^http/i, "ws") + "/bind-vscode";
    const bind = new BindConnection(wsURL, info.token, this.output);

    const disposables: vscode.Disposable[] = [];
    webviewPanel.onDidDispose(
      () => {
        for (const d of disposables) d.dispose();
        void bind.close();
        stop(filePath);
      },
      null,
      disposables,
    );
  }
}

function placeholderHTML(body: string): string {
  return `<!DOCTYPE html><html><head><meta charset="UTF-8"><style>
    body{font-family:var(--vscode-font-family,sans-serif);color:var(--vscode-foreground);
         background:var(--vscode-editor-background);padding:2rem;}
  </style></head><body>${body}</body></html>`;
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}
