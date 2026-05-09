import { proxyRequest } from '../../server/proxy.js';

export async function loader({ request, params, context }) {
  return proxyRequest(request, `/v1/${params['*'] || ''}`, { routeContext: context });
}

export async function action({ request, params, context }) {
  return proxyRequest(request, `/v1/${params['*'] || ''}`, { routeContext: context });
}
