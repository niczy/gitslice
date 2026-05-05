import { useQuery } from '@tanstack/react-query';
import { apiBaseUrl, fetchWithAuth } from '../utils/api.js';
import { normalizeSliceInfo } from '../utils/normalize.js';

async function fetchSlices() {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/slices?limit=200`);
  if (!response.ok) {
    throw new Error(`Request failed (${response.status})`);
  }
  const payload = await response.json();
  return (payload.slices || []).map(normalizeSliceInfo);
}

export function useSlicesQuery(initialSlices) {
  const hasInitialSlices = Array.isArray(initialSlices);
  return useQuery({
    queryKey: ['slices'],
    queryFn: fetchSlices,
    initialData: hasInitialSlices ? initialSlices : undefined,
    refetchOnMount: hasInitialSlices ? 'always' : true,
    staleTime: 15_000,
  });
}
