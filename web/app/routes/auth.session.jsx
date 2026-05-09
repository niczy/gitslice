import { handleSessionRequest } from '../../server/auth.js';

export async function loader({ request, context }) {
  return handleSessionRequest(request, { routeContext: context });
}
