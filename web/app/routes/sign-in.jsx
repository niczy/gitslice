import { useLoaderData } from 'react-router';
import { SignIn } from '@clerk/react-router';
import { isClerkAuthConfigured } from '../../server/auth.js';

export async function loader({ request }) {
  const url = new URL(request.url);
  return {
    configured: isClerkAuthConfigured(),
    redirectURL: String(url.searchParams.get('redirect_url') || '/slices').trim() || '/slices',
  };
}

export default function ClerkSignInRoute() {
  const { configured, redirectURL } = useLoaderData();
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
        <SignIn
          path="/sign-in"
          routing="path"
          signUpUrl="/sign-up"
          forceRedirectUrl={redirectURL}
          fallbackRedirectUrl={redirectURL}
        />
      </div>
    </main>
  );
}
