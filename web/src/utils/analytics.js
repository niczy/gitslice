export function trackRouteEvent(eventName, properties = {}) {
  window.dispatchEvent(
    new CustomEvent('app:route-analytics', {
      detail: {
        event: eventName,
        properties,
      },
    }),
  );
}
