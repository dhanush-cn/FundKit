// FundKit Control Center — shared API types.
// Engineered by Dhanush C N (github.com/dhanush-cn)

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
  amount: number;
  type: OrderType;
  status: OrderStatus;
  idempotency_key: string;
  created_at: string;
  updated_at: string;
}

export interface CreateOrderPayload {
  user_id: string;
  fund_id: string;
  amount: number;
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
