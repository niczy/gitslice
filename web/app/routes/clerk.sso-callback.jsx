import { AuthenticateWithRedirectCallback } from '@clerk/react';
import { useLoaderData } from 'react-router';

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
  const redirectURL = normalizeRedirectURL(
    request,
    url.searchParams.get('sign_in_force_redirect_url')
      || url.searchParams.get('sign_up_force_redirect_url')
      || url.searchParams.get('redirect_url')
      || '/slices',
    '/slices',
  );
  return { redirectURL };
}

export default function ClerkSSOCallbackRoute() {
  const { redirectURL } = useLoaderData();
  return (
    <AuthenticateWithRedirectCallback
      signInForceRedirectUrl={redirectURL}
      signInFallbackRedirectUrl={redirectURL}
      signUpForceRedirectUrl={redirectURL}
      signUpFallbackRedirectUrl={redirectURL}
    />
  );
}
