import { handleDevLogoutRequest } from '../../server/auth.js';

export async function action({ request }) {
  return handleDevLogoutRequest(request);
}
