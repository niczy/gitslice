import { AGENT_EVENTS_CACHE_LIMIT } from './agentConstants.js';

const DB_NAME = 'gitslice-agent-events';
const DB_VERSION = 1;
const STORE_NAME = 'session-events';
const CACHE_KEY_PREFIX = 'agent-events:v1:';
const WRITE_DEBOUNCE_MS = 750;

const memoryCache = new Map();
const pendingWrites = new Map();
let dbPromise = null;

function cacheKey(sessionId) {
  return `${CACHE_KEY_PREFIX}${String(sessionId || '').trim()}`;
}

function cloneEvent(event) {
  return {
    seq: Number(event?.seq || 0),
    ts: event?.ts || '',
    stream: event?.stream || '',
    type: event?.type || '',
    kind: event?.kind || '',
    payload: event?.payload ?? {},
  };
}

export function normalizeCachedAgentEvents(events, limit = AGENT_EVENTS_CACHE_LIMIT) {
  if (!Array.isArray(events) || events.length === 0) {
    return [];
  }
  const bySeq = new Map();
  for (const event of events) {
    const normalized = cloneEvent(event);
    if (!Number.isFinite(normalized.seq) || normalized.seq <= 0) {
      continue;
    }
    bySeq.set(normalized.seq, normalized);
  }
  return Array.from(bySeq.values())
    .sort((a, b) => a.seq - b.seq)
    .slice(-Math.max(1, limit || AGENT_EVENTS_CACHE_LIMIT));
}

export function mergeCachedAgentEvents(currentEvents, incomingEvents, limit = AGENT_EVENTS_CACHE_LIMIT) {
  return normalizeCachedAgentEvents([
    ...(Array.isArray(currentEvents) ? currentEvents : []),
    ...(Array.isArray(incomingEvents) ? incomingEvents : []),
  ], limit);
}

function indexedDBAvailable() {
  return typeof window !== 'undefined' && window.indexedDB;
}

function requestToPromise(request) {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error || new Error('IndexedDB request failed'));
  });
}

async function openDB() {
  if (!indexedDBAvailable()) {
    return null;
  }
  if (!dbPromise) {
    dbPromise = new Promise((resolve, reject) => {
      const request = window.indexedDB.open(DB_NAME, DB_VERSION);
      request.onupgradeneeded = () => {
        const db = request.result;
        if (!db.objectStoreNames.contains(STORE_NAME)) {
          db.createObjectStore(STORE_NAME, { keyPath: 'key' });
        }
      };
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => reject(request.error || new Error('Unable to open agent event cache'));
    }).catch(() => null);
  }
  return dbPromise;
}

async function withStore(mode, callback) {
  const db = await openDB();
  if (!db) {
    return null;
  }
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, mode);
    const store = tx.objectStore(STORE_NAME);
    let callbackResult;
    tx.oncomplete = () => resolve(callbackResult);
    tx.onerror = () => reject(tx.error || new Error('Agent event cache transaction failed'));
    tx.onabort = () => reject(tx.error || new Error('Agent event cache transaction aborted'));
    callbackResult = callback(store);
  });
}

export async function readCachedAgentSessionEvents(sessionId) {
  const key = cacheKey(sessionId);
  if (!key || key === CACHE_KEY_PREFIX) {
    return [];
  }
  const memoryEvents = memoryCache.get(key);
  if (memoryEvents) {
    return normalizeCachedAgentEvents(memoryEvents);
  }

  try {
    const record = await withStore('readonly', (store) => requestToPromise(store.get(key)));
    const events = normalizeCachedAgentEvents(record?.events || []);
    if (events.length > 0) {
      memoryCache.set(key, events);
    }
    return events;
  } catch {
    return [];
  }
}

export async function writeCachedAgentSessionEvents(sessionId, events) {
  const key = cacheKey(sessionId);
  if (!key || key === CACHE_KEY_PREFIX) {
    return [];
  }
  const normalized = normalizeCachedAgentEvents(events);
  memoryCache.set(key, normalized);
  try {
    await withStore('readwrite', (store) => {
      store.put({
        key,
        events: normalized,
        updatedAt: new Date().toISOString(),
      });
    });
  } catch {
    // The in-memory cache still keeps tab switching fast when persistent cache is unavailable.
  }
  return normalized;
}

export function scheduleCachedAgentSessionEventsWrite(sessionId, events) {
  const key = cacheKey(sessionId);
  if (!key || key === CACHE_KEY_PREFIX) {
    return;
  }
  const normalized = normalizeCachedAgentEvents(events);
  memoryCache.set(key, normalized);

  if (typeof window === 'undefined') {
    void writeCachedAgentSessionEvents(sessionId, normalized);
    return;
  }
  const pending = pendingWrites.get(key);
  if (pending) {
    window.clearTimeout(pending);
  }
  const timeoutId = window.setTimeout(() => {
    pendingWrites.delete(key);
    void writeCachedAgentSessionEvents(sessionId, normalized);
  }, WRITE_DEBOUNCE_MS);
  pendingWrites.set(key, timeoutId);
}
