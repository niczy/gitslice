import { useLoaderData } from 'react-router';
import { ClerkProvider, SignIn } from '@clerk/react-router';

export async function loader({ request }) {
  const url = new URL(request.url);
  return {
    publishableKey: String(process.env.CLERK_PUBLISHABLE_KEY || process.env.VITE_CLERK_PUBLISHABLE_KEY || '').trim(),
    redirectURL: String(url.searchParams.get('redirect_url') || '/browser').trim() || '/browser',
  };
}

export default function ClerkSignInRoute() {
  const { publishableKey, redirectURL } = useLoaderData();
  if (!publishableKey) {
    return (
      <main className="section">
        <div className="panel-error">Clerk publishable key is not configured.</div>
      </main>
    );
  }

  return (
    <main className="section auth-page">
      <ClerkProvider publishableKey={publishableKey}>
        <div className="flex justify-center">
          <SignIn
            path="/sign-in"
            routing="path"
            signUpUrl="/sign-up"
            forceRedirectUrl={redirectURL}
            fallbackRedirectUrl={redirectURL}
          />
        </div>
      </ClerkProvider>
    </main>
  );
}
