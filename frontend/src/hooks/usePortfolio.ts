// Engineered by Dhanush C N (github.com/dhanush-cn)
import { useCallback, useEffect, useRef, useState } from 'react';

import { ApiError, apiRequest, describeError } from '../lib/api';
import type { PnLResponse } from '../types';

export interface UsePortfolio {
  pnl: PnLResponse | null;
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
  reset: () => void;
}

interface UsePortfolioOptions {
  /** Gate on the signed-in session the same way useOrders and useServiceHealth do. */
  enabled: boolean;
  /**
   * The account this widget shows P&L for. This is always the signed-in
   * user's own id -- there is deliberately no way to pass an arbitrary id in
   * from outside, because the whole point of this hook is that a customer can
   * only ever see their own portfolio. The backend enforces the same rule
   * independently (the gateway-verified identity wins over anything in the
   * URL), so this is defense in depth, not the only guard.
   */
  userId: string | null;
  pollIntervalMs?: number;
  onUnauthorized?: () => void;
}

/**
 * usePortfolio reads the P&L view, which the gateway serves by way of the
 * order-service to portfolio-service gRPC call, for the signed-in user.
 *
 * It follows the same polling shape as useOrders: fetch on mount/login, then
 * again on an interval, so the panel reflects "real-time" P&L without the
 * user having to ask for it -- there is no separate subscribe step because
 * the transport is polled HTTP, not a socket.
 *
 * A 503 from the order-service -> portfolio-service chain is reported as a
 * dependency outage rather than as a generic failure, because the distinction
 * is what the operator needs. A 401 clears the session through the same path
 * every other authenticated hook uses, rather than surfacing as an on-panel
 * error.
 */
export function usePortfolio({
  enabled,
  userId,
  pollIntervalMs = 15000,
  onUnauthorized,
}: UsePortfolioOptions): UsePortfolio {
  const [pnl, setPnl] = useState<PnLResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const inFlight = useRef<AbortController | null>(null);

  // Held in a ref for the same reason useOrders does this: changing the
  // callback must not restart the polling effect below.
  const unauthorizedRef = useRef(onUnauthorized);
  useEffect(() => {
    unauthorizedRef.current = onUnauthorized;
  }, [onUnauthorized]);

  const refresh = useCallback(async () => {
    if (!userId) {
      return;
    }

    inFlight.current?.abort();
    const controller = new AbortController();
    inFlight.current = controller;

    setLoading(true);
    try {
      const response = await apiRequest<PnLResponse>(
        `/portfolio/${encodeURIComponent(userId)}/pnl`,
        { signal: controller.signal },
      );
      // The backend serializes this from a protobuf-generated Go struct whose
      // fields are all `json:"...,omitempty"`. encoding/json drops a key
      // entirely when its value is the zero value -- an empty/nil holdings
      // slice, or total_value/total_unrealized_gain when they're exactly 0
      // (e.g. a user with no open positions). Normalize here so the render
      // path never has to guess whether a field is "0" or "missing", and a
      // zero-position account renders a clean ₹0.00 rather than a blank gap.
      setPnl({
        user_id: response.user_id ?? userId,
        total_value: response.total_value ?? 0,
        total_unrealized_gain: response.total_unrealized_gain ?? 0,
        holdings: response.holdings ?? [],
      });
      setError(null);
    } catch (cause) {
      if (cause instanceof DOMException && cause.name === 'AbortError') {
        return;
      }
      if (cause instanceof ApiError && cause.isUnauthorized) {
        setPnl(null);
        unauthorizedRef.current?.();
        return;
      }
      if (cause instanceof ApiError && cause.status === 503) {
        setError('Portfolio service is unavailable right now. Try again shortly.');
      } else {
        setError(describeError(cause, 'Unable to load portfolio P&L'));
      }
      setPnl(null);
    } finally {
      if (inFlight.current === controller) {
        setLoading(false);
      }
    }
  }, [userId]);

  useEffect(() => {
    if (!enabled || !userId) {
      setPnl(null);
      setError(null);
      return;
    }

    void refresh();
    const timer = window.setInterval(() => void refresh(), pollIntervalMs);
    return () => {
      window.clearInterval(timer);
      inFlight.current?.abort();
    };
  }, [enabled, userId, pollIntervalMs, refresh]);

  const reset = useCallback(() => {
    setPnl(null);
    setError(null);
  }, []);

  return { pnl, loading, error, refresh, reset };
}
