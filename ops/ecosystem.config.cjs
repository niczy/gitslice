const fs = require("node:fs");
const path = require("node:path");

const repoRoot = "/home/nic/workspace/gitslice";
const opsEnvPath = path.join(repoRoot, "ops/.env");

function loadEnvFile(filePath) {
  if (!fs.existsSync(filePath)) {
    return {};
  }

  const env = {};
  const lines = fs.readFileSync(filePath, "utf8").split(/\r?\n/);
  for (const rawLine of lines) {
    const line = rawLine.trim();
    if (!line || line.startsWith("#")) {
      continue;
    }

    const normalized = line.startsWith("export ") ? line.slice(7) : line;
    const separatorIndex = normalized.indexOf("=");
    if (separatorIndex <= 0) {
      continue;
    }

    const key = normalized.slice(0, separatorIndex).trim();
    let value = normalized.slice(separatorIndex + 1).trim();
    if (
      (value.startsWith('"') && value.endsWith('"')) ||
      (value.startsWith("'") && value.endsWith("'"))
    ) {
      value = value.slice(1, -1);
    }
    env[key] = value;
  }
  return env;
}

const fileEnv = loadEnvFile(opsEnvPath);

const coreEnv = {
  CORE_SERVICE_PORT: fileEnv.CORE_SERVICE_PORT || "50051",
  GATEWAY_PORT: fileEnv.GATEWAY_PORT || "8080",
  STORAGE_TYPE: fileEnv.STORAGE_TYPE || "postgres",
  POSTGRES_DSN: fileEnv.POSTGRES_DSN || "postgres://nic@127.0.0.1:55432/gitslice?sslmode=disable",
  SKIP_GIT_POPULATION: fileEnv.SKIP_GIT_POPULATION || "1",
  OBJECT_STORE_TYPE: fileEnv.OBJECT_STORE_TYPE || "filesystem",
  OBJECT_STORE_DIR: fileEnv.OBJECT_STORE_DIR || path.join(repoRoot, ".objectstore"),
  PUBLIC_WEB_BASE_URL: fileEnv.PUBLIC_WEB_BASE_URL || "https://agenttools.dev",
};

const webEnv = {
  VITE_FILE_API_PROXY_TARGET: fileEnv.VITE_FILE_API_PROXY_TARGET || "http://127.0.0.1:8080",
  VITE_WEB_AGENT_REAL_RUNTIME: fileEnv.VITE_WEB_AGENT_REAL_RUNTIME,
  AUTH_SECRET: fileEnv.AUTH_SECRET,
  AUTH_GOOGLE_ID: fileEnv.AUTH_GOOGLE_ID,
  AUTH_GOOGLE_SECRET: fileEnv.AUTH_GOOGLE_SECRET,
  AUTH_GITHUB_ID: fileEnv.AUTH_GITHUB_ID,
  AUTH_GITHUB_SECRET: fileEnv.AUTH_GITHUB_SECRET,
};

module.exports = {
  apps: [
    {
      name: "gitslice-core",
      script: "./core_server",
      cwd: repoRoot,
      autorestart: true,
      max_restarts: 10,
      out_file: path.join(repoRoot, "logs/pm2-core.out.log"),
      error_file: path.join(repoRoot, "logs/pm2-core.err.log"),
      env: coreEnv,
    },
    {
      name: "gitslice-web",
      script: "npm",
      args: "run preview -- --host 127.0.0.1 --port 4173",
      cwd: path.join(repoRoot, "web"),
      autorestart: true,
      max_restarts: 10,
      out_file: path.join(repoRoot, "logs/pm2-web.out.log"),
      error_file: path.join(repoRoot, "logs/pm2-web.err.log"),
      env: webEnv,
    },
  ],
};
