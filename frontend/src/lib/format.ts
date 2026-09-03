// Engineered by Dhanush C N (github.com/dhanush-cn)
import type { OrderStatus } from '../types';

const currencyFormatter = new Intl.NumberFormat('en-IN', {
  style: 'currency',
  currency: 'INR',
  maximumFractionDigits: 0,
});

export function formatCurrency(amount: number): string {
  return currencyFormatter.format(Number.isFinite(amount) ? amount : 0);
}

export function formatDate(value: string): string {
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime())
    ? value
    : parsed.toLocaleString('en-IN', { dateStyle: 'medium', timeStyle: 'short' });
}

export function statusTone(status: OrderStatus): string {
  switch (status) {
    case 'PENDING':
      return 'pending';
    case 'PROCESSING':
      return 'processing';
    case 'EXECUTED':
      return 'executed';
    case 'FAILED':
      return 'failed';
    default:
      return 'pending';
  }
}
