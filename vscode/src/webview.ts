// Minimal HTML wrapper that embeds the webui inside an iframe.

export function webviewHTML(url: string, token: string): string {
  const full = url + "/?token=" + encodeURIComponent(token);
  return `<!DOCTYPE html>
<html>
<head>
  <meta charset="UTF-8">
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
