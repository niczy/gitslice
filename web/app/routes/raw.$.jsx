import { handleRawContentRequest } from '../../server/raw-content.js';

export async function loader({ request, params, context }) {
  return handleRawContentRequest(request, params['*'] || '', { routeContext: context });
}

export async function action({ request, params, context }) {
  return handleRawContentRequest(request, params['*'] || '', { routeContext: context });
}
