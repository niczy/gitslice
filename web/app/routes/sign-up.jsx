import { useLoaderData } from 'react-router';
import { SignUp } from '@clerk/react-router';
import { isClerkAuthConfigured } from '../../server/auth.js';

function normalizeRedirectURL(request, rawValue, fallbackPath = '/slices') {
  const fallbackURL = new URL(fallbackPath, request.url);
  const candidate = String(rawValue || '').trim();
  if (!candidate) {
    return fallbackURL.toString();
  }
  try {
    const resolved = new URL(candidate, request.url);
    return resolved.origin === fallbackURL.origin ? resolved.toString() : fallbackURL.toString();
  } catch {
    return fallbackURL.toString();
  }
}

export async function loader({ request }) {
  const url = new URL(request.url);
  const redirectURL = normalizeRedirectURL(request, url.searchParams.get('redirect_url'), '/slices');
  return {
    configured: isClerkAuthConfigured(),
    redirectURL,
    signInURL: `/sign-in?redirect_url=${encodeURIComponent(redirectURL)}`,
  };
}

export default function ClerkSignUpRoute() {
  const { configured, redirectURL, signInURL } = useLoaderData();
  if (!configured) {
    return (
      <main className="section">
        <div className="panel-error">Clerk is not fully configured.</div>
      </main>
    );
  }

  return (
    <main className="section auth-page">
      <div className="flex justify-center">
        <SignUp
          path="/sign-up"
          routing="path"
          signInUrl={signInURL}
          forceRedirectUrl={redirectURL}
          fallbackRedirectUrl={redirectURL}
        />
      </div>
    </main>
  );
}
