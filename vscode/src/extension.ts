// Extension entry point. Registers the custom editor + a utility command.

import * as vscode from "vscode";
import { MeadsEditorProvider } from "./customEditor";
import { stopAll } from "./subprocess";

let output: vscode.OutputChannel | undefined;

export function activate(context: vscode.ExtensionContext): void {
  output = vscode.window.createOutputChannel("Meads");
  context.subscriptions.push(output);

  const provider = new MeadsEditorProvider(output);
  context.subscriptions.push(
    vscode.window.registerCustomEditorProvider(
      MeadsEditorProvider.viewType,
      provider,
      {
        webviewOptions: { retainContextWhenHidden: true },
        supportsMultipleEditorsPerDocument: false,
      },
    ),
  );

  // "Open with default editor" — opens TASKS.md in the raw text editor.
  context.subscriptions.push(
    vscode.commands.registerCommand("meads.openWithDefault", async () => {
      const active = vscode.window.activeTextEditor?.document.uri;
      if (!active) {
        await vscode.window.showInformationMessage(
          "No active editor. Open a TASKS file first.",
        );
        return;
      }
      await vscode.commands.executeCommand(
        "vscode.openWith",
        active,
        "default",
      );
    }),
  );

  output.appendLine("meads: extension activated");
}

export function deactivate(): void {
  stopAll();
  output?.appendLine("meads: extension deactivated");
}
