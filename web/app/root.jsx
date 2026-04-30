import { useState } from 'react';
import {
  isRouteErrorResponse,
  Links,
  Meta,
  Outlet,
  Scripts,
  ScrollRestoration,
  useLoaderData,
} from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ClerkProvider } from '@clerk/react-router';
import { clerkMiddleware, rootAuthLoader } from '@clerk/react-router/server';

import '../src/index.css';
import '../src/styles.css';

function getClerkEnv() {
  const authProvider = String(process.env.AUTH_PROVIDER || 'local').trim().toLowerCase();
  const secretKey = String(process.env.CLERK_SECRET_KEY || '').trim();
  const publishableKey = String(process.env.CLERK_PUBLISHABLE_KEY || process.env.VITE_CLERK_PUBLISHABLE_KEY || '').trim();
  return {
    enabled: authProvider === 'clerk' && Boolean(secretKey && publishableKey),
    secretKey,
    publishableKey,
  };
}

export const middleware = [
  async (args, next) => {
    const clerk = getClerkEnv();
    if (!clerk.enabled) {
      return next();
    }
    return clerkMiddleware({
      secretKey: clerk.secretKey,
      publishableKey: clerk.publishableKey,
      signInUrl: '/sign-in',
      signUpUrl: '/sign-up',
    })(args, next);
  },
];

export async function loader(args) {
  const clerk = getClerkEnv();
  if (!clerk.enabled) {
    return {};
  }
  return rootAuthLoader(args, {
    secretKey: clerk.secretKey,
    publishableKey: clerk.publishableKey,
    signInUrl: '/sign-in',
    signUpUrl: '/sign-up',
  });
}

function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        refetchOnWindowFocus: false,
      },
    },
  });
}

export function Layout({ children }) {
  const [queryClient] = useState(createQueryClient);

  return (
    <html lang="en">
      <head>
        <meta charSet="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <Meta />
        <Links />
      </head>
      <body>
        <QueryClientProvider client={queryClient}>
          {children}
        </QueryClientProvider>
        <ScrollRestoration />
        <Scripts />
      </body>
    </html>
  );
}

export default function Root() {
  const loaderData = useLoaderData();
  if (loaderData?.clerkState) {
    return (
      <ClerkProvider loaderData={loaderData}>
        <Outlet />
      </ClerkProvider>
    );
  }
  return <Outlet />;
}

export function ErrorBoundary({ error }) {
  let title = 'Unexpected error';
  let message = 'The requested page could not be rendered.';

  if (isRouteErrorResponse(error)) {
    title = error.status === 404 ? 'Page not found' : `Error ${error.status}`;
    message = error.statusText || message;
  } else if (error instanceof Error) {
    message = error.message;
  }

  return (
    <main className="section">
      <div className="panel-error">
        <strong>{title}</strong>
        <div>{message}</div>
      </div>
    </main>
  );
}
