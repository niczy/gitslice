const textEncoder = new TextEncoder();
const textDecoder = new TextDecoder();
const hmacKeyCache = new Map();

function requireCrypto() {
  if (!globalThis.crypto?.subtle || typeof globalThis.crypto?.getRandomValues !== 'function') {
    throw new Error('Web Crypto APIs are not available in the current runtime');
  }
  return globalThis.crypto;
}

function normalizeEnvValue(value) {
  return String(value || '').trim();
}

export function getConfiguredAPIBaseURL(envLike, fallback = '') {
  return (
    normalizeEnvValue(envLike?.PUBLIC_API_BASE_URL) ||
    normalizeEnvValue(envLike?.VITE_FILE_API_BASE_URL) ||
    normalizeEnvValue(envLike?.VITE_FILE_API_PROXY_TARGET) ||
    normalizeEnvValue(fallback)
  );
}

export function utf8ToBytes(value) {
  return textEncoder.encode(String(value ?? ''));
}

export function bytesToUTF8(bytes) {
  return textDecoder.decode(bytes);
}

export function bytesToBase64(bytes) {
  if (!bytes?.length) {
    return '';
  }
  let binary = '';
  const chunkSize = 0x8000;
  for (let index = 0; index < bytes.length; index += chunkSize) {
    const chunk = bytes.subarray(index, index + chunkSize);
    binary += String.fromCharCode(...chunk);
  }
  return globalThis.btoa(binary);
}

export function base64ToBytes(value) {
  const normalized = normalizeEnvValue(value);
  if (!normalized) {
    return new Uint8Array();
  }
  const binary = globalThis.atob(normalized);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return bytes;
}

export function bytesToBase64URL(bytes) {
  return bytesToBase64(bytes)
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/g, '');
}

export function base64URLToBytes(value) {
  let normalized = normalizeEnvValue(value)
    .replace(/-/g, '+')
    .replace(/_/g, '/');
  while (normalized.length % 4 !== 0) {
    normalized += '=';
  }
  return base64ToBytes(normalized);
}

export function encodeBase64URLUTF8(value) {
  return bytesToBase64URL(utf8ToBytes(value));
}

export function decodeBase64URLUTF8(value) {
  return bytesToUTF8(base64URLToBytes(value));
}

export function decodeBase64UTF8(value) {
  return bytesToUTF8(base64ToBytes(value));
}

export function randomHex(byteCount = 2) {
  const bytes = new Uint8Array(byteCount);
  requireCrypto().getRandomValues(bytes);
  return Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join('');
}

async function importHMACKey(secret) {
  const normalized = String(secret ?? '');
  if (hmacKeyCache.has(normalized)) {
    return hmacKeyCache.get(normalized);
  }
  const promise = requireCrypto().subtle.importKey(
    'raw',
    utf8ToBytes(normalized),
    { name: 'HMAC', hash: 'SHA-256' },
    false,
    ['sign'],
  );
  hmacKeyCache.set(normalized, promise);
  return promise;
}

export async function signHMACSHA256Base64URL(secret, value) {
  const key = await importHMACKey(secret);
  const signature = await requireCrypto().subtle.sign('HMAC', key, utf8ToBytes(value));
  return bytesToBase64URL(new Uint8Array(signature));
}

export function timingSafeEqualText(left, right) {
  const leftBytes = utf8ToBytes(left);
  const rightBytes = utf8ToBytes(right);
  const maxLength = Math.max(leftBytes.length, rightBytes.length);
  let diff = leftBytes.length ^ rightBytes.length;
  for (let index = 0; index < maxLength; index += 1) {
    diff |= (leftBytes[index] || 0) ^ (rightBytes[index] || 0);
  }
  return diff === 0;
}
