import { handleSessionRequest } from '../../server/auth.js';

export async function loader({ request }) {
  return handleSessionRequest(request);
}
