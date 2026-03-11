# Git Slice Web

A React Router framework-mode web app for Git Slice with server-side rendering, resource routes for `/auth/*`, and a server proxy path for `/v1/*` when running directly on the web port.

## Getting started

1. Install dependencies:

   ```bash
   npm install
   ```

2. Run the SSR dev server:

   ```bash
   npm run dev
   ```

3. Create a production build:

   ```bash
   npm run build
   ```

4. Start the production server locally after building:

   ```bash
   npm run start -- --host 127.0.0.1 --port 4173
   ```

5. Run end-to-end tests (builds and starts the SSR server automatically):

   ```bash
   npm run test:e2e
   ```

The production output is `build/client` plus `build/server`, served by `react-router-serve`.
