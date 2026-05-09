import { handleAuthRequest } from '../../server/auth.js';

export async function loader({ request, context }) {
  return handleAuthRequest(request, { routeContext: context });
}

export async function action({ request, context }) {
  return handleAuthRequest(request, { routeContext: context });
}
