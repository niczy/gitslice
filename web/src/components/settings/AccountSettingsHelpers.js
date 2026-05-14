export function formatAuthMethodType(value) {
  const normalized = String(value || '').trim();
  switch (normalized) {
    case 'AUTH_METHOD_TYPE_PASSWORD':
    case '1':
      return 'password';
    case 'AUTH_METHOD_TYPE_OAUTH':
    case '2':
      return 'oauth';
    case 'AUTH_METHOD_TYPE_SAML':
    case '3':
      return 'saml';
    default:
      return normalized || 'unknown';
  }
}
