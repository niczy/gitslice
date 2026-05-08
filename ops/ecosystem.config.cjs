const fs = require("node:fs");
const path = require("node:path");

const repoRoot = "/home/nic/workspace/gitslice";
const opsDir = path.join(repoRoot, "ops");
const productionEnvPath = path.join(opsDir, ".env.production");
const stagingEnvPath = path.join(opsDir, ".env.staging");

function loadEnvFile(filePath) {
  if (!filePath || !fs.existsSync(filePath)) {
    return null;
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

function resolveEnvConfig(target, defaults) {
  if (target === "staging") {
    const env = loadEnvFile(stagingEnvPath);
    if (!env) {
      return null;
    }
    return env;
  }

  const env = loadEnvFile(productionEnvPath) || {};
  return Object.keys(env).length === 0 ? defaults : env;
}

function coreDefaults(target) {
  if (target === "staging") {
    return {
      DEPLOY_ENV: "staging",
      CORE_BIND_ADDR: "127.0.0.1",
      CORE_SERVICE_PORT: "50052",
      STORAGE_TYPE: "postgres",
      POSTGRES_DSN: "postgres://nic@127.0.0.1:55432/gitslice_staging?sslmode=disable",
      SKIP_GIT_POPULATION: "1",
      OBJECT_STORE_TYPE: "r2",
      R2_REGION: "auto",
      R2_PREFIX: "staging",
      PUBLIC_WEB_BASE_URL: "https://agenttools.dev",
      PUBLIC_API_BASE_URL: "https://api.agenttools.dev",
      WEB_DEPLOY_TARGET: "cloudflare_worker",
      WEB_COMPAT_RUNTIME: "worker",
      WEB_HOST: "127.0.0.1",
      WEB_PORT: "4174",
      RUN_WEB_SSR: "0",
    };
  }

  return {
    DEPLOY_ENV: "production",
    CORE_BIND_ADDR: "127.0.0.1",
    CORE_SERVICE_PORT: "50051",
    STORAGE_TYPE: "postgres",
    POSTGRES_DSN: "postgres://nic@127.0.0.1:55432/gitslice?sslmode=disable",
    SKIP_GIT_POPULATION: "1",
    OBJECT_STORE_TYPE: "r2",
    R2_REGION: "auto",
    R2_PREFIX: "production",
    PUBLIC_WEB_BASE_URL: "https://gitslice.io",
    PUBLIC_API_BASE_URL: "https://api.gitslice.io",
    WEB_DEPLOY_TARGET: "cloudflare_worker",
    WEB_COMPAT_RUNTIME: "worker",
    WEB_HOST: "127.0.0.1",
    WEB_PORT: "4173",
    RUN_WEB_SSR: "0",
  };
}

function buildCoreEnv(target, fileEnv) {
  const defaults = coreDefaults(target);
  return {
    DEPLOY_ENV: fileEnv.DEPLOY_ENV || defaults.DEPLOY_ENV,
    CORE_BIND_ADDR: fileEnv.CORE_BIND_ADDR || defaults.CORE_BIND_ADDR,
    CORE_SERVICE_PORT: fileEnv.CORE_SERVICE_PORT || defaults.CORE_SERVICE_PORT,
    AUTH_PROVIDER: fileEnv.AUTH_PROVIDER || "local",
    AUTH_SECRET: fileEnv.AUTH_SECRET || "",
    ALLOW_LEGACY_USER_AUTH: fileEnv.ALLOW_LEGACY_USER_AUTH || "",
    ADMIN_USER_EMAILS: fileEnv.ADMIN_USER_EMAILS || "",
    STORAGE_TYPE: fileEnv.STORAGE_TYPE || defaults.STORAGE_TYPE,
    POSTGRES_DSN: fileEnv.POSTGRES_DSN || fileEnv.NEON_DB || defaults.POSTGRES_DSN,
    POSTGRES_MAX_CONNS: fileEnv.POSTGRES_MAX_CONNS || "",
    POSTGRES_MIN_CONNS: fileEnv.POSTGRES_MIN_CONNS || "",
    POSTGRES_MAX_CONN_LIFETIME: fileEnv.POSTGRES_MAX_CONN_LIFETIME || "",
    POSTGRES_PROMOTION_MAX_CONNS: fileEnv.POSTGRES_PROMOTION_MAX_CONNS || "",
    SKIP_GIT_POPULATION: fileEnv.SKIP_GIT_POPULATION || defaults.SKIP_GIT_POPULATION,
    OBJECT_STORE_TYPE: fileEnv.OBJECT_STORE_TYPE || defaults.OBJECT_STORE_TYPE,
    OBJECT_STORE_DIR: fileEnv.OBJECT_STORE_DIR || path.join(repoRoot, ".objectstore"),
    GCS_BUCKET: fileEnv.GCS_BUCKET || "",
    GCS_ENDPOINT: fileEnv.GCS_ENDPOINT || "",
    GCS_CREDENTIALS_FILE: fileEnv.GCS_CREDENTIALS_FILE || "",
    GCS_CREDENTIALS_JSON: fileEnv.GCS_CREDENTIALS_JSON || "",
    GCS_DISABLE_AUTH: fileEnv.GCS_DISABLE_AUTH || "",
    R2_ENDPOINT: fileEnv.R2_ENDPOINT || "",
    R2_REGION: fileEnv.R2_REGION || defaults.R2_REGION,
    R2_BUCKET: fileEnv.R2_BUCKET || "",
    R2_PREFIX: fileEnv.R2_PREFIX || defaults.R2_PREFIX,
    R2_ACCESS_KEY_ID: fileEnv.R2_ACCESS_KEY_ID || "",
    R2_SECRET_ACCESS_KEY: fileEnv.R2_SECRET_ACCESS_KEY || "",
    R2_USE_PATH_STYLE: fileEnv.R2_USE_PATH_STYLE || "",
    CLERK_SECRET_KEY: fileEnv.CLERK_SECRET_KEY || "",
    CLERK_PUBLISHABLE_KEY: fileEnv.CLERK_PUBLISHABLE_KEY || fileEnv.VITE_CLERK_PUBLISHABLE_KEY || "",
    CLERK_JWT_KEY: fileEnv.CLERK_JWT_KEY || "",
    CLERK_WEBHOOK_SECRET: fileEnv.CLERK_WEBHOOK_SECRET || "",
    PUBLIC_WEB_BASE_URL: fileEnv.PUBLIC_WEB_BASE_URL || defaults.PUBLIC_WEB_BASE_URL,
    PUBLIC_API_BASE_URL: fileEnv.PUBLIC_API_BASE_URL || defaults.PUBLIC_API_BASE_URL,
    WEB_DEPLOY_TARGET: fileEnv.WEB_DEPLOY_TARGET || defaults.WEB_DEPLOY_TARGET,
    WEB_COMPAT_RUNTIME: fileEnv.WEB_COMPAT_RUNTIME || defaults.WEB_COMPAT_RUNTIME,
  };
}

function appendAppsForTarget(apps, target) {
  const fileEnv = resolveEnvConfig(target, coreDefaults(target));
  if (!fileEnv) {
    return;
  }

  const coreEnv = buildCoreEnv(target, fileEnv);
  apps.push({
    name: `gitslice-core-${target}`,
    script: "./core_server",
    cwd: repoRoot,
    autorestart: true,
    max_restarts: 10,
    out_file: path.join(repoRoot, `logs/pm2-core-${target}.out.log`),
    error_file: path.join(repoRoot, `logs/pm2-core-${target}.err.log`),
    env: coreEnv,
  });
}

const apps = [];
appendAppsForTarget(apps, "production");
appendAppsForTarget(apps, "staging");

module.exports = { apps };
