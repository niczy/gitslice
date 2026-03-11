import { useQuery } from '@tanstack/react-query';
import { fetchAgentCapabilities } from '../utils/api.js';

export function useAgentCapabilitiesQuery(enabled) {
  return useQuery({
    queryKey: ['agent-capabilities'],
    queryFn: fetchAgentCapabilities,
    enabled,
    staleTime: 5 * 60_000,
    retry: 1,
  });
}
