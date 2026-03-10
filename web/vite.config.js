import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

import { authJsMiddlewarePlugin } from './auth-middleware.js';

const fileGatewayTarget = process.env.VITE_FILE_API_PROXY_TARGET || 'http://localhost:8080';

export default defineConfig({
  plugins: [react(), authJsMiddlewarePlugin({ gatewayTarget: fileGatewayTarget })],
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
