// Sync vscode/package.json#version from the git tag ($GITHUB_REF_NAME).
// Strips a leading "v". No-op if GITHUB_REF_NAME is unset.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const pkgPath = path.join(here, "..", "package.json");

const ref = process.env.GITHUB_REF_NAME;
if (!ref) {
  console.log("sync-version: GITHUB_REF_NAME unset, skipping");
  process.exit(0);
}
const version = ref.replace(/^v/, "");
if (!/^\d+\.\d+\.\d+/.test(version)) {
  console.error(`sync-version: ${ref} does not look like a semver tag; skipping`);
  process.exit(0);
}

const pkg = JSON.parse(fs.readFileSync(pkgPath, "utf8"));
pkg.version = version;
fs.writeFileSync(pkgPath, JSON.stringify(pkg, null, 2) + "\n");
console.log(`sync-version: set version = ${version}`);
