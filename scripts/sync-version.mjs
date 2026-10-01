// Copies the version from package.json (managed by Changesets) into
// Cargo.toml and Cargo.lock. With --check it only reports a mismatch.
import { readFileSync, writeFileSync } from "node:fs";

const check = process.argv.includes("--check");
const { version } = JSON.parse(readFileSync("package.json", "utf8"));

const files = [
  // The first `version` line in Cargo.toml belongs to [package].
  { path: "Cargo.toml", pattern: /^(version = ")[^"]*(")/m },
  { path: "Cargo.lock", pattern: /(\[\[package\]\]\nname = "proxemby"\nversion = ")[^"]*(")/ },
];

let mismatched = false;
for (const { path, pattern } of files) {
  const text = readFileSync(path, "utf8");
  if (!pattern.test(text)) {
    console.error(`${path}: proxemby version not found`);
    process.exit(1);
  }
  const updated = text.replace(pattern, `$1${version}$2`);
  if (updated === text) {
    continue;
  }
  if (check) {
    console.error(`${path}: version does not match package.json (${version}); run node scripts/sync-version.mjs`);
    mismatched = true;
  } else {
    writeFileSync(path, updated);
    console.log(`${path}: set version to ${version}`);
  }
}
process.exit(mismatched ? 1 : 0);
