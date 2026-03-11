import { handleAuthRequest } from '../../server/auth.js';

export async function loader({ request }) {
  return handleAuthRequest(request);
}

export async function action({ request }) {
  return handleAuthRequest(request);
}
