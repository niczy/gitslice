export function trackRouteEvent(eventName, metadata = {}) {
  const payload = {
    eventName,
    ...metadata,
    timestamp: new Date().toISOString(),
  };

  window.dispatchEvent(new CustomEvent('gitslice:route-analytics', { detail: payload }));

  if (typeof window.gtag === 'function') {
    window.gtag('event', eventName, metadata);
  }
}
