import { useLoaderData } from 'react-router';
import { SignIn } from '@clerk/react-router';

export async function loader({ request }) {
  const url = new URL(request.url);
  const secretKey = String(process.env.CLERK_SECRET_KEY || '').trim();
  const publishableKey = String(process.env.CLERK_PUBLISHABLE_KEY || process.env.VITE_CLERK_PUBLISHABLE_KEY || '').trim();
  return {
    configured: Boolean(secretKey && publishableKey),
    redirectURL: String(url.searchParams.get('redirect_url') || '/browser').trim() || '/browser',
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
