import { proxyRequest } from '../../server/proxy.js';

export async function loader({ request, params }) {
  return proxyRequest(request, `/v1/${params['*'] || ''}`);
}

export async function action({ request, params }) {
  return proxyRequest(request, `/v1/${params['*'] || ''}`);
}
