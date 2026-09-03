// Engineered by Dhanush C N (github.com/dhanush-cn)
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import { ApiError, apiRequest, describeError } from '../lib/api';
import type { CreateOrderPayload, Order, OrderStatus } from '../types';

export interface OrderMetrics {
  totalInvested: number;
  pending: number;
  processing: number;
  executed: number;
  failed: number;
}

export interface UseOrders {
  orders: Order[];
  metrics: OrderMetrics;
  loading: boolean;
  submitting: boolean;
  error: string | null;
  clearError: () => void;
  refresh: () => Promise<void>;
  placeOrder: (payload: CreateOrderPayload) => Promise<boolean>;
  updateStatus: (orderId: string, status: OrderStatus) => Promise<boolean>;
}

interface UseOrdersOptions {
  enabled: boolean;
  pollIntervalMs?: number;
  onUnauthorized?: () => void;
}

/**
 * useOrders owns the order book: fetching, polling, mutation and the derived
 * counters the dashboard renders.
 *
 * In-flight requests are aborted when the hook unmounts or a newer refresh
 * starts, so a slow response can never overwrite fresher state.
 */
export function useOrders({ enabled, pollIntervalMs = 15000, onUnauthorized }: UseOrdersOptions): UseOrders {
  const [orders, setOrders] = useState<Order[]>([]);
  const [loading, setLoading] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const inFlight = useRef<AbortController | null>(null);

  // The callback is held in a ref so that changing it does not restart the
  // polling effect. Writing it during render would be a side effect in the
  // render phase, so the sync happens in its own effect.
  const unauthorizedRef = useRef(onUnauthorized);
  useEffect(() => {
    unauthorizedRef.current = onUnauthorized;
  }, [onUnauthorized]);

  const handleFailure = useCallback((cause: unknown, fallback: string) => {
    if (cause instanceof DOMException && cause.name === 'AbortError') {
      return;
    }
    if (cause instanceof ApiError && cause.isUnauthorized) {
      unauthorizedRef.current?.();
      return;
    }
    setError(describeError(cause, fallback));
  }, []);

  const refresh = useCallback(async () => {
    inFlight.current?.abort();
    const controller = new AbortController();
    inFlight.current = controller;

    setLoading(true);
    try {
      const list = await apiRequest<Order[]>('/orders', { signal: controller.signal });
      setOrders(
        [...(list ?? [])].sort((left, right) => right.created_at.localeCompare(left.created_at)),
      );
      setError(null);
    } catch (cause) {
      handleFailure(cause, 'Unable to load orders');
    } finally {
      if (inFlight.current === controller) {
        setLoading(false);
      }
    }
  }, [handleFailure]);

  useEffect(() => {
    if (!enabled) {
      return;
    }

    void refresh();
    const timer = window.setInterval(() => void refresh(), pollIntervalMs);
    return () => {
      window.clearInterval(timer);
      inFlight.current?.abort();
    };
  }, [enabled, pollIntervalMs, refresh]);

  const placeOrder = useCallback(
    async (payload: CreateOrderPayload) => {
      setSubmitting(true);
      try {
        await apiRequest<Order>('/orders', { method: 'POST', body: payload });
        setError(null);
        await refresh();
        return true;
      } catch (cause) {
        handleFailure(cause, 'Unable to place order');
        return false;
      } finally {
        setSubmitting(false);
      }
    },
    [handleFailure, refresh],
  );

  const updateStatus = useCallback(
    async (orderId: string, status: OrderStatus) => {
      try {
        await apiRequest<Order>(`/orders/${encodeURIComponent(orderId)}`, {
          method: 'PATCH',
          body: { status },
        });
        setError(null);
        await refresh();
        return true;
      } catch (cause) {
        handleFailure(cause, 'Unable to update order');
        return false;
      }
    },
    [handleFailure, refresh],
  );

  // Signing out must not leave the previous session's orders on screen. Deriving
  // the visible list keeps that a render-time concern rather than an effect that
  // clears state and triggers a second render.
  const visibleOrders = useMemo(() => (enabled ? orders : []), [enabled, orders]);

  const metrics = useMemo<OrderMetrics>(() => {
    return visibleOrders.reduce<OrderMetrics>(
      (accumulator, order) => {
        accumulator.totalInvested += order.amount;
        switch (order.status) {
          case 'PENDING':
            accumulator.pending += 1;
            break;
          case 'PROCESSING':
            accumulator.processing += 1;
            break;
          case 'EXECUTED':
            accumulator.executed += 1;
            break;
          case 'FAILED':
            accumulator.failed += 1;
            break;
        }
        return accumulator;
      },
      { totalInvested: 0, pending: 0, processing: 0, executed: 0, failed: 0 },
    );
  }, [visibleOrders]);

  const clearError = useCallback(() => setError(null), []);

  return {
    orders: visibleOrders,
    metrics,
    loading,
    submitting,
    error,
    clearError,
    refresh,
    placeOrder,
    updateStatus,
  };
}
