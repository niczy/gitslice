import { useLoaderData, useLocation, useNavigate } from 'react-router';

import App from '../../src/App.jsx';
import { setCachedSession } from '../../src/auth.js';
import { parseLocation, resolveHomeRouteForSession } from '../../src/utils/routing.js';
import { getPublicAuthConfig, loadSession } from '../../server/auth.js';
import { loadBrowserRouteData } from '../../server/browser-data.js';

function buildNotFoundRouteInfo(requestURL) {
  const pathname = String(requestURL.pathname || '/').replace(/^\/+/, '');
  return {
    page: 'not-found',
    commitHash: '',
    changesetId: '',
    unknownPath: `${pathname}${requestURL.search || ''}`,
  };
}

export async function loader({ request }) {
  let session = null;
  let sessionError = '';
  try {
    session = await loadSession(request);
  } catch (error) {
    sessionError = error instanceof Error ? error.message : 'Failed to load browser session.';
  }
  const requestURL = new URL(request.url);
  const requestRouteInfo = parseLocation(requestURL);
  let routeInfo = resolveHomeRouteForSession(requestRouteInfo, session);
  let browserRoute = await loadBrowserRouteData(request, session, routeInfo);
  if (browserRoute.authExpired) {
    session = null;
    sessionError = '';
    const fallbackRouteInfo = resolveHomeRouteForSession(requestRouteInfo, session);
    const fallbackBrowserRoute = await loadBrowserRouteData(request, session, fallbackRouteInfo);
    routeInfo = fallbackRouteInfo;
    browserRoute = {
      data: fallbackBrowserRoute.data,
      setCookies: [...(browserRoute.setCookies || []), ...(fallbackBrowserRoute.setCookies || [])],
    };
  }
  let responseStatus = 200;
  if (browserRoute.data?.routeNotFound) {
    routeInfo = buildNotFoundRouteInfo(requestURL);
    responseStatus = 404;
  }
  const browserData = { ...(browserRoute.data || {}) };
  delete browserData.routeNotFound;
  const response = Response.json({
    session,
    sessionError,
    routeInfo,
    authConfig: getPublicAuthConfig(request),
    browserData,
  }, { status: responseStatus });
  for (const cookie of browserRoute.setCookies || []) {
    if (cookie) {
      response.headers.append('Set-Cookie', cookie);
    }
  }
  return response;
}

export default function AppShellRoute() {
  const { session, sessionError, routeInfo: loaderRouteInfo, authConfig, browserData } = useLoaderData();
  const location = useLocation();
  const navigate = useNavigate();
  const locationRouteInfo = parseLocation(location);
  const routeInfo = locationRouteInfo.legacyHash ? locationRouteInfo : loaderRouteInfo || locationRouteInfo;

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
