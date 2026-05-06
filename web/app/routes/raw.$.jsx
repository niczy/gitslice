import { handleRawContentRequest } from '../../server/raw-content.js';

export async function loader({ request, params }) {
  return handleRawContentRequest(request, params['*'] || '');
}

export async function action({ request, params }) {
  return handleRawContentRequest(request, params['*'] || '');
}
