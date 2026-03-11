import { useQuery } from '@tanstack/react-query';
import { fetchOAuthSession } from '../auth.js';

export function useWebSession() {
  return useQuery({
    queryKey: ['web-session'],
    queryFn: fetchOAuthSession,
    staleTime: 30_000,
    retry: false,
    refetchOnWindowFocus: true,
  });
}
