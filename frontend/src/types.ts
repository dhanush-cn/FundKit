// FundKit Control Center — shared API types.
// Engineered by Dhanush C N (github.com/dhanush-cn)

/**
 * An amount in paise — 1/100 of a rupee. ₹100.50 is 10050.
 *
 * order-service stores, transports and compares money as an integer so that
 * arithmetic on it is exact. The alias carries no runtime weight; it is here so
 * that every money field on the wire says what unit it is in, and so a reviewer
 * seeing `Paise` next to a call to formatCurrency (which takes rupees) spots
 * the mistake by reading.
 */
export type Paise = number;

export type OrderStatus = 'PENDING' | 'PROCESSING' | 'EXECUTED' | 'FAILED';
export type OrderType = 'SIP' | 'LUMPSUM';
export type ServiceState = 'UP' | 'DOWN';

export interface Order {
  id: string;
  user_id: string;
  user_name?: string;
  user_email?: string;
  user_phone?: string;
  fund_id: string;
  amount: Paise;
  type: OrderType;
  status: OrderStatus;
  idempotency_key: string;
  created_at: string;
  updated_at: string;
}

export interface CreateOrderPayload {
  user_id: string;
  fund_id: string;
  /** Integer paise. Send 10050 for ₹100.50 — a decimal is rejected with 400. */
  amount: Paise;
  type: OrderType;
  idempotency_key: string;
}

export interface ServiceHealthItem {
  service: string;
  url: string;
  status: ServiceState;
  latency?: string;
  error?: string;
}

export interface ServiceHealthResponse {
  status: 'UP' | 'DEGRADED';
  services: ServiceHealthItem[];
}

/**
 * Portfolio figures arrive as RUPEE decimals, not paise.
 *
 * portfolio-service holds these as integer paise internally, but its gRPC
 * contract declares them `double` and converts at the boundary. Until that
 * contract has a breaking release, this half of the API stays in rupees — which
 * is exactly why the two units have separate formatters in lib/format.ts.
 */
export interface PnLHolding {
  fund_id: string;
  fund_name: string;
  units: number;
  invested_amount: number;
  nav: number;
  current_value: number;
  unrealized_gain: number;
}

export interface PnLResponse {
  user_id: string;
  total_value: number;
  total_unrealized_gain: number;
  holdings: PnLHolding[];
}

/** The account as the gateway exposes it. The password hash never leaves the server. */
export interface User {
  id: string;
  username: string;
  email: string;
  phone: string;
  full_name: string;
}

export interface SessionResponse {
  token: string;
  user_id: string;
  expires_at: string;
  user: User;
}

export interface LoginPayload {
  username: string;
  password: string;
}

export interface RegisterPayload {
  username: string;
  email: string;
  phone: string;
  full_name: string;
  password: string;
}
