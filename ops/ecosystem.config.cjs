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

function parseBoolean(value, fallback) {
  if (value === undefined || value === null || value === "") {
    return fallback;
  }
  switch (String(value).trim().toLowerCase()) {
    case "1":
    case "true":
    case "yes":
    case "on":
      return true;
    case "0":
    case "false":
    case "no":
    case "off":
      return false;
    default:
      return fallback;
  }
}

const fileEnv = loadEnvFile(opsEnvPath);
const shouldRunWebSSR = parseBoolean(
  fileEnv.RUN_WEB_SSR,
  (fileEnv.WEB_DEPLOY_TARGET || "node") === "node",
);

const coreEnv = {
  DEPLOY_ENV: fileEnv.DEPLOY_ENV || "production",
  CORE_BIND_ADDR: fileEnv.CORE_BIND_ADDR || "127.0.0.1",
  CORE_SERVICE_PORT: fileEnv.CORE_SERVICE_PORT || "50051",
  STORAGE_TYPE: fileEnv.STORAGE_TYPE || "postgres",
  POSTGRES_DSN: fileEnv.POSTGRES_DSN || "postgres://nic@127.0.0.1:55432/gitslice?sslmode=disable",
  POSTGRES_MAX_CONNS: fileEnv.POSTGRES_MAX_CONNS || "",
  POSTGRES_MIN_CONNS: fileEnv.POSTGRES_MIN_CONNS || "",
  POSTGRES_MAX_CONN_LIFETIME: fileEnv.POSTGRES_MAX_CONN_LIFETIME || "",
  SKIP_GIT_POPULATION: fileEnv.SKIP_GIT_POPULATION || "1",
  OBJECT_STORE_TYPE: fileEnv.OBJECT_STORE_TYPE || "filesystem",
  OBJECT_STORE_DIR: fileEnv.OBJECT_STORE_DIR || path.join(repoRoot, ".objectstore"),
  GCS_BUCKET: fileEnv.GCS_BUCKET || "",
  GCS_ENDPOINT: fileEnv.GCS_ENDPOINT || "",
  GCS_CREDENTIALS_FILE: fileEnv.GCS_CREDENTIALS_FILE || "",
  GCS_CREDENTIALS_JSON: fileEnv.GCS_CREDENTIALS_JSON || "",
  GCS_DISABLE_AUTH: fileEnv.GCS_DISABLE_AUTH || "",
  R2_ENDPOINT: fileEnv.R2_ENDPOINT || "",
  R2_REGION: fileEnv.R2_REGION || "auto",
  R2_BUCKET: fileEnv.R2_BUCKET || "",
  R2_PREFIX: fileEnv.R2_PREFIX || "",
  R2_ACCESS_KEY_ID: fileEnv.R2_ACCESS_KEY_ID || "",
  R2_SECRET_ACCESS_KEY: fileEnv.R2_SECRET_ACCESS_KEY || "",
  R2_USE_PATH_STYLE: fileEnv.R2_USE_PATH_STYLE || "",
  PUBLIC_WEB_BASE_URL: fileEnv.PUBLIC_WEB_BASE_URL || "https://gitslice.io",
  PUBLIC_API_BASE_URL: fileEnv.PUBLIC_API_BASE_URL || "",
  WEB_DEPLOY_TARGET: fileEnv.WEB_DEPLOY_TARGET || "node",
  WEB_COMPAT_RUNTIME: fileEnv.WEB_COMPAT_RUNTIME || "node",
};

const webEnv = {
  DEPLOY_ENV: fileEnv.DEPLOY_ENV || "production",
  HOST: fileEnv.WEB_HOST || "127.0.0.1",
  PORT: fileEnv.WEB_PORT || "4173",
  PUBLIC_WEB_BASE_URL: fileEnv.PUBLIC_WEB_BASE_URL || "https://gitslice.io",
  PUBLIC_API_BASE_URL: fileEnv.PUBLIC_API_BASE_URL || "",
  WEB_DEPLOY_TARGET: fileEnv.WEB_DEPLOY_TARGET || "node",
  WEB_COMPAT_RUNTIME: fileEnv.WEB_COMPAT_RUNTIME || "node",
  VITE_FILE_API_BASE_URL:
    fileEnv.VITE_FILE_API_BASE_URL ||
    fileEnv.PUBLIC_API_BASE_URL ||
    "",
  VITE_FILE_API_PROXY_TARGET:
    fileEnv.PUBLIC_API_BASE_URL ||
    fileEnv.VITE_FILE_API_BASE_URL ||
    fileEnv.VITE_FILE_API_PROXY_TARGET ||
    "http://127.0.0.1:50051",
  VITE_WEB_AGENT_REAL_RUNTIME: fileEnv.VITE_WEB_AGENT_REAL_RUNTIME,
  AUTH_SECRET: fileEnv.AUTH_SECRET,
  AUTH_GOOGLE_ID: fileEnv.AUTH_GOOGLE_ID,
  AUTH_GOOGLE_SECRET: fileEnv.AUTH_GOOGLE_SECRET,
  AUTH_GITHUB_ID: fileEnv.AUTH_GITHUB_ID,
  AUTH_GITHUB_SECRET: fileEnv.AUTH_GITHUB_SECRET,
};
const apps = [
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
];

if (shouldRunWebSSR) {
  apps.push({
    name: "gitslice-web",
    script: "npm",
    args: "run start",
    cwd: path.join(repoRoot, "web"),
    autorestart: true,
    max_restarts: 10,
    out_file: path.join(repoRoot, "logs/pm2-web.out.log"),
    error_file: path.join(repoRoot, "logs/pm2-web.err.log"),
    env: webEnv,
  });
}

module.exports = { apps };
