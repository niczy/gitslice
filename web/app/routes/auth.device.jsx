import { renderDevicePageResponse } from '../../server/auth.js';

export async function loader({ request }) {
  return renderDevicePageResponse(request);
}
