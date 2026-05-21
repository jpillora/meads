// Minimal HTML wrapper that embeds the webui inside an iframe.

export function webviewHTML(url: string, token: string): string {
  const full = url + "/?token=" + encodeURIComponent(token);
  // Lock the webview down explicitly so a future tightening of VS Code's
  // default webview CSP can't silently break the iframe. frame-src is
  // dynamic: http://127.0.0.1:<port> locally, or the tunneled
  // https://...preview... origin under Remote-SSH / Codespaces.
  const frameOrigin = new URL(url).origin;
  const csp =
    "default-src 'none'; " +
    "style-src 'unsafe-inline'; " +
    `frame-src ${frameOrigin};`;
  return `<!DOCTYPE html>
<html>
<head>
  <meta charset="UTF-8">
  <meta http-equiv="Content-Security-Policy" content="${csp}">
  <title>Meads</title>
  <style>
    html, body, iframe { margin: 0; padding: 0; height: 100%; width: 100%; border: 0; }
    body { background: var(--vscode-editor-background); }
    iframe { display: block; }
  </style>
</head>
<body>
  <iframe src="${escapeHtml(full)}" allow="clipboard-read; clipboard-write"></iframe>
</body>
</html>`;
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}
