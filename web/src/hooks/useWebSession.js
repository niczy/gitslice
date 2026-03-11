import { useQuery } from '@tanstack/react-query';
import { fetchOAuthSession } from '../auth.js';

export function useWebSession(initialData = undefined) {
  return useQuery({
    queryKey: ['web-session'],
    queryFn: fetchOAuthSession,
    initialData,
    staleTime: 30_000,
    retry: false,
    refetchOnWindowFocus: true,
  });
}
