import { proxyRequest } from '../../server/proxy.js';

export async function loader({ request, context }) {
  return proxyRequest(request, '/ws/agent-runners', { routeContext: context });
}
