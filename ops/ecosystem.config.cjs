module.exports = {
  apps: [
    {
      name: "gitslice-core",
      script: "./core_server",
      cwd: "/home/nic/workspace/gitslice",
      autorestart: true,
      max_restarts: 10,
      out_file: "/home/nic/workspace/gitslice/logs/pm2-core.out.log",
      error_file: "/home/nic/workspace/gitslice/logs/pm2-core.err.log",
      env: {
        CORE_SERVICE_PORT: "50051",
        GATEWAY_PORT: "8080",
        STORAGE_TYPE: process.env.STORAGE_TYPE || "postgres",
        POSTGRES_DSN: process.env.POSTGRES_DSN || "postgresql://nic@127.0.0.1:55432/gitslice?sslmode=disable",
        OBJECT_STORE_TYPE: process.env.OBJECT_STORE_TYPE || "filesystem",
        OBJECT_STORE_DIR: process.env.OBJECT_STORE_DIR || "/home/nic/workspace/gitslice/.objectstore"
      }
    },
    {
      name: "gitslice-web",
      script: "npm",
      args: "run preview -- --host 127.0.0.1 --port 4173",
      cwd: "/home/nic/workspace/gitslice/web",
      autorestart: true,
      max_restarts: 10,
      out_file: "/home/nic/workspace/gitslice/logs/pm2-web.out.log",
      error_file: "/home/nic/workspace/gitslice/logs/pm2-web.err.log"
    }
  ]
};
