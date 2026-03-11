import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

import { authJsMiddlewarePlugin } from './auth-middleware.js';

const fileGatewayTarget = process.env.VITE_FILE_API_PROXY_TARGET || 'http://localhost:8080';

function spaPathRoutingPlugin() {
  const handler = (req, _res, next) => {
    const method = String(req.method || 'GET').toUpperCase();
    if (!['GET', 'HEAD'].includes(method)) {
      next();
      return;
    }

    const rawURL = String(req.url || '/');
    const pathname = rawURL.split('?')[0];
    const isAssetLike = /\.[a-z0-9]+$/i.test(pathname);
    const isFrameworkPath = pathname.startsWith('/@');
    const isAPIPath = pathname.startsWith('/v1') || pathname.startsWith('/auth');

    if (isAssetLike || isFrameworkPath || isAPIPath) {
      next();
      return;
    }

    req.url = '/';
    next();
  };

  return {
    name: 'spa-path-routing',
    configureServer(server) {
      server.middlewares.use(handler);
    },
    configurePreviewServer(server) {
      server.middlewares.use(handler);
    },
  };
}

export default defineConfig({
  plugins: [react(), authJsMiddlewarePlugin({ gatewayTarget: fileGatewayTarget }), spaPathRoutingPlugin()],
  server: {
    proxy: {
      '/v1': {
        target: fileGatewayTarget,
        changeOrigin: true,
      },
    },
  },
  preview: {
    allowedHosts: ['agenttools.dev'],
    proxy: {
      '/v1': {
        target: fileGatewayTarget,
        changeOrigin: true,
      },
    },
  },
});
