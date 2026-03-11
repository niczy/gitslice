import { useLoaderData, useLocation, useNavigate } from 'react-router';

import App from '../../src/App.jsx';
import { setCachedSession } from '../../src/auth.js';
import { parseLocation } from '../../src/utils/routing.js';
import { loadSession } from '../../server/auth.js';

export async function loader({ request }) {
  return {
    session: await loadSession(request),
  };
}

export default function AppShellRoute() {
  const { session } = useLoaderData();
  const location = useLocation();
  const navigate = useNavigate();
  const routeInfo = parseLocation(location);

  if (typeof document !== 'undefined') {
    setCachedSession(session || null);
  }

  return (
    <App
      initialRoute={routeInfo}
      initialSession={session || null}
      routerNavigate={navigate}
    />
  );
}
