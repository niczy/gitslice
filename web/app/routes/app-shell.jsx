import { useLoaderData, useLocation, useNavigate } from 'react-router';

import App from '../../src/App.jsx';
import { setCachedSession } from '../../src/auth.js';
import { parseLocation } from '../../src/utils/routing.js';
import { getPublicAuthConfig, loadSession } from '../../server/auth.js';

export async function loader({ request }) {
  let session = null;
  let sessionError = '';
  try {
    session = await loadSession(request);
  } catch (error) {
    sessionError = error instanceof Error ? error.message : 'Failed to load browser session.';
  }
  return {
    session,
    sessionError,
    authConfig: getPublicAuthConfig(request),
  };
}

export default function AppShellRoute() {
  const { session, sessionError, authConfig } = useLoaderData();
  const location = useLocation();
  const navigate = useNavigate();
  const routeInfo = parseLocation(location);

  if (typeof document !== 'undefined') {
    setCachedSession(session || null);
  }

  return (
    <App
      initialRoute={routeInfo}
      initialAuthConfig={authConfig || { authProvider: 'local', allowDevLogin: true }}
      initialSession={session || null}
      initialSessionError={sessionError || ''}
      routerNavigate={navigate}
    />
  );
}
