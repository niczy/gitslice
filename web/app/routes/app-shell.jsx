import { useLoaderData, useLocation, useNavigate } from 'react-router';

import App from '../../src/App.jsx';
import { setCachedSession } from '../../src/auth.js';
import { parseLocation } from '../../src/utils/routing.js';
import { getPublicAuthConfig, loadSession } from '../../server/auth.js';
import { loadBrowserRouteData } from '../../server/browser-data.js';

export async function loader({ request }) {
  let session = null;
  let sessionError = '';
  try {
    session = await loadSession(request);
  } catch (error) {
    sessionError = error instanceof Error ? error.message : 'Failed to load browser session.';
  }
  const requestURL = new URL(request.url);
  const routeInfo = parseLocation(requestURL);
  const browserRoute = await loadBrowserRouteData(request, session, routeInfo);
  if (browserRoute.authExpired) {
    session = null;
    sessionError = '';
  }
  const response = Response.json({
    session,
    sessionError,
    authConfig: getPublicAuthConfig(request),
    browserData: browserRoute.data,
  });
  for (const cookie of browserRoute.setCookies || []) {
    if (cookie) {
      response.headers.append('Set-Cookie', cookie);
    }
  }
  return response;
}

export default function AppShellRoute() {
  const { session, sessionError, authConfig, browserData } = useLoaderData();
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
      initialBrowserData={browserData || null}
      routerNavigate={navigate}
    />
  );
}
