import { handleDevLoginRequest } from '../../server/auth.js';

export async function action({ request }) {
  return handleDevLoginRequest(request);
}
