// Cross-platform launcher for an app instance under test. Playwright's webServer
// commands must run on Windows, macOS and Linux, so this replaces the previous
// bash-only `cd .. && go build && rm && exec` chain.
//
// Usage: node run-server.mjs <binary-name> <db-name> [extra app args...]
// Env (ADDR, ANTHROPIC_*) is passed through by Playwright; DB_PATH is set here
// to an absolute path under .bin/ so it never depends on the working directory.
import { spawnSync, spawn } from "node:child_process";
import { rmSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const e2eDir = dirname(fileURLToPath(import.meta.url));
const repoRoot = join(e2eDir, "..");
const binDir = join(e2eDir, ".bin");

const [binName, dbName, ...appArgs] = process.argv.slice(2);
if (!binName || !dbName) {
  console.error("usage: run-server.mjs <binary-name> <db-name> [app args...]");
  process.exit(2);
}
const binExt = process.platform === "win32" ? ".exe" : "";
const binPath = join(binDir, binName + binExt);

// Build the app binary from the repo root.
const build = spawnSync("go", ["build", "-o", binPath, "."], {
  cwd: repoRoot,
  stdio: "inherit",
});
if (build.status !== 0) process.exit(build.status ?? 1);

// Start from a clean database so each run is deterministic.
const dbPath = join(binDir, dbName);
for (const suffix of ["", "-shm", "-wal"]) {
  rmSync(dbPath + suffix, { force: true });
}

// Run the server in the foreground so Playwright manages its lifecycle. Set an
// absolute DB_PATH so it never depends on the process's working directory.
const server = spawn(binPath, appArgs, {
  stdio: "inherit",
  env: { ...process.env, DB_PATH: dbPath },
});
server.on("exit", (code) => process.exit(code ?? 0));
for (const sig of ["SIGINT", "SIGTERM"]) {
  process.on(sig, () => server.kill(sig));
}
