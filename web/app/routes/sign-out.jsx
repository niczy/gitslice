import { handleDevLogoutRequest, isClerkAuthConfigured } from '../../server/auth.js';
import { useEffect } from 'react';
import { useLoaderData } from 'react-router';
import { ClerkLoaded, ClerkLoading, useClerk } from '@clerk/react';

export async function loader({ request }) {
  const url = new URL(request.url);
  const localLogoutResponse = handleDevLogoutRequest(request);
  const headers = new Headers();
  for (const cookie of localLogoutResponse.headers.getSetCookie?.() || []) {
    headers.append('Set-Cookie', cookie);
  }
  return Response.json({
    configured: isClerkAuthConfigured(),
    redirectURL: String(url.searchParams.get('redirect_url') || '/').trim() || '/',
  }, { headers });
}

export function headers({ loaderHeaders }) {
  const setCookie = loaderHeaders.getSetCookie?.() || [];
  return setCookie.length > 0 ? { 'Set-Cookie': setCookie } : {};
}

function ClerkSignOutEffect({ redirectURL }) {
  const clerk = useClerk();

  useEffect(() => {
    const fallback = window.setTimeout(() => {
      if (typeof window !== 'undefined') {
        window.location.assign(redirectURL);
      }
    }, 5000);

    Promise.resolve(clerk.signOut({ redirectUrl: redirectURL })).catch(() => {
      if (typeof window !== 'undefined') {
        window.location.assign(redirectURL);
      }
    }).finally(() => {
      window.clearTimeout(fallback);
    });

    return () => {
      window.clearTimeout(fallback);
    };
  }, [clerk, redirectURL]);

  return (
    <main className="section">
      <div className="panel-empty">Signing out…</div>
    </main>
  );
}

export default function ClerkSignOutRoute() {
  const { configured, redirectURL } = useLoaderData();
  if (!configured) {
    return (
      <main className="section">
        <div className="panel-empty">Signing out…</div>
      </main>
    );
  }
  return (
    <>
      <ClerkLoading>
        <main className="section">
          <div className="panel-empty">Signing out…</div>
        </main>
      </ClerkLoading>
      <ClerkLoaded>
        <ClerkSignOutEffect redirectURL={redirectURL} />
      </ClerkLoaded>
    </>
  );
}
