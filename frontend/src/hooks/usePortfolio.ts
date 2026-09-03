// Engineered by Dhanush C N (github.com/dhanush-cn)
import { useCallback, useState } from 'react';

import { ApiError, apiRequest, describeError } from '../lib/api';
import type { PnLResponse } from '../types';

export interface UsePortfolio {
  pnl: PnLResponse | null;
  loading: boolean;
  error: string | null;
  fetchPnL: (userId: string) => Promise<void>;
  reset: () => void;
}

/**
 * usePortfolio reads the P&L view, which the gateway serves by way of the
 * order-service to portfolio-service gRPC call.
 *
 * A 503 from that chain is reported as a dependency outage rather than as a
 * generic failure, because the distinction is what the operator needs.
 */
export function usePortfolio(): UsePortfolio {
  const [pnl, setPnl] = useState<PnLResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const fetchPnL = useCallback(async (userId: string) => {
    const trimmed = userId.trim();
    if (!trimmed) {
      setError('Enter a user id to fetch P&L');
      return;
    }

    setLoading(true);
    setError(null);
    try {
      const response = await apiRequest<PnLResponse>(
        `/portfolio/${encodeURIComponent(trimmed)}/pnl`,
      );
      setPnl(response);
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 503) {
        setError('Portfolio service is unavailable right now. Try again shortly.');
      } else {
        setError(describeError(cause, 'Unable to load portfolio P&L'));
      }
      setPnl(null);
    } finally {
      setLoading(false);
    }
  }, []);

  const reset = useCallback(() => {
    setPnl(null);
    setError(null);
  }, []);

  return { pnl, loading, error, fetchPnL, reset };
}
