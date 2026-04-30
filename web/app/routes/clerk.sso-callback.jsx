import { AuthenticateWithRedirectCallback } from '@clerk/react';
import { useLoaderData } from 'react-router';

export async function loader({ request }) {
  const url = new URL(request.url);
  const redirectURL = String(
    url.searchParams.get('sign_in_force_redirect_url')
      || url.searchParams.get('sign_up_force_redirect_url')
      || url.searchParams.get('redirect_url')
      || '/browser',
  ).trim() || '/browser';
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
