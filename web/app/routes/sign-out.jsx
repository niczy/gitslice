import { useEffect } from 'react';
import { useLoaderData } from 'react-router';
import { ClerkProvider, useClerk } from '@clerk/react-router';

export async function loader({ request }) {
  const url = new URL(request.url);
  return {
    publishableKey: String(process.env.CLERK_PUBLISHABLE_KEY || process.env.VITE_CLERK_PUBLISHABLE_KEY || '').trim(),
    redirectURL: String(url.searchParams.get('redirect_url') || '/').trim() || '/',
  };
}

function ClerkSignOutEffect({ redirectURL }) {
  const clerk = useClerk();

  useEffect(() => {
    clerk.signOut({ redirectUrl: redirectURL }).catch(() => {
      if (typeof window !== 'undefined') {
        window.location.assign(redirectURL);
      }
    });
  }, [clerk, redirectURL]);

  return (
    <main className="section">
      <div className="panel-empty">Signing out…</div>
    </main>
  );
}

export default function ClerkSignOutRoute() {
  const { publishableKey, redirectURL } = useLoaderData();
  if (!publishableKey) {
    return (
      <main className="section">
        <div className="panel-empty">Signing out…</div>
      </main>
    );
  }
  return (
    <ClerkProvider publishableKey={publishableKey}>
      <ClerkSignOutEffect redirectURL={redirectURL} />
    </ClerkProvider>
  );
}
