// Build script for the meads VS Code extension.
// Bundles src/extension.ts → dist/extension.js using esbuild.

import { build, context } from "esbuild";

const watch = process.argv.includes("--watch");

const config = {
  entryPoints: ["src/extension.ts"],
  bundle: true,
  outfile: "dist/extension.js",
  platform: "node",
  target: "node18",
  format: "cjs",
  sourcemap: true,
  minify: false,
  external: ["vscode"],
  logLevel: "info",
};

if (watch) {
  const ctx = await context(config);
  await ctx.watch();
  console.log("esbuild: watching…");
} else {
  await build(config);
}
