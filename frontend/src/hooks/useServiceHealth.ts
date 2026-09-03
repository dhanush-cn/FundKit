// Engineered by Dhanush C N (github.com/dhanush-cn)
import { useCallback, useEffect, useMemo, useState } from 'react';

import { apiRequest, describeError } from '../lib/api';
import type { ServiceHealthItem, ServiceHealthResponse } from '../types';

export interface UseServiceHealth {
  services: ServiceHealthItem[];
  allHealthy: boolean;
  error: string | null;
  refresh: () => Promise<void>;
}

/**
 * useServiceHealth polls the gateway's aggregated stack status.
 *
 * The gateway answers 206 when a dependency is down, which the fetch wrapper
 * treats as success — a degraded stack is information, not an error.
 */
export function useServiceHealth(enabled: boolean, pollIntervalMs = 15000): UseServiceHealth {
  const [services, setServices] = useState<ServiceHealthItem[]>([]);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      const response = await apiRequest<ServiceHealthResponse>('/services/health');
      setServices(response.services ?? []);
      setError(null);
    } catch (cause) {
      setError(describeError(cause, 'Unable to read service health'));
      setServices([]);
    }
  }, []);

  useEffect(() => {
    if (!enabled) {
      return;
    }

    void refresh();
    const timer = window.setInterval(() => void refresh(), pollIntervalMs);
    return () => window.clearInterval(timer);
  }, [enabled, pollIntervalMs, refresh]);

  // Derived rather than cleared in an effect, so signing out hides the previous
  // session's readings in the same render.
  const visibleServices = useMemo(() => (enabled ? services : []), [enabled, services]);

  const allHealthy = useMemo(
    () => visibleServices.length > 0 && visibleServices.every((service) => service.status === 'UP'),
    [visibleServices],
  );

  return { services: visibleServices, allHealthy, error, refresh };
}
