// Engineered by Dhanush C N (github.com/dhanush-cn)
import type { OrderStatus } from '../types';

// Two currency formatters, because the API speaks two units and pretending
// otherwise is how a display bug becomes a 100x display bug.
//
//   - order-service sends `amount` as integer PAISE. Its ledger is exact and
//     the wire format preserves that.
//   - portfolio-service sends valuations as RUPEE doubles, because its gRPC
//     contract declares them `double`. Its internals are integer paise now too,
//     but the .proto is a published contract and changing it is a separate,
//     breaking release.
//
// So there are two entry points, named after their unit, and no function that
// takes "a number" and guesses. Anything reading a value off the wire has to
// pick one, which forces the question to be answered at the call site.

const rupeeFormatter = new Intl.NumberFormat('en-IN', {
  style: 'currency',
  currency: 'INR',
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
});

/**
 * Formats a RUPEE amount — a decimal figure, as portfolio-service reports.
 */
export function formatCurrency(rupees: number): string {
  return rupeeFormatter.format(Number.isFinite(rupees) ? rupees : 0);
}

/**
 * Formats an integer PAISE amount, as order-service reports. 10050 → ₹100.50.
 */
export function formatPaise(paise: number): string {
  return formatCurrency(paiseToRupees(paise));
}

/**
 * Converts paise to rupees for display only. The result is a float and must
 * never be sent back to the API.
 */
export function paiseToRupees(paise: number): number {
  return Number.isFinite(paise) ? paise / 100 : 0;
}

/**
 * Converts what a human typed into exact integer paise.
 *
 * A string input is parsed digit by digit rather than through parseFloat,
 * because parseFloat introduces the very error the integer representation
 * exists to avoid — `8.115 * 100` is 811.4999999999999 in IEEE-754, which
 * rounds to the wrong paise. Splitting on the decimal point and padding the
 * fractional half keeps the conversion exact for every value a form can
 * produce. This mirrors domain.ParseRupees on the Go side.
 *
 * Returns NaN for anything that is not a valid amount, so the caller can
 * refuse to submit rather than posting a silent zero.
 */
export function rupeesToPaise(input: string | number): number {
  const text = String(input).trim().replace(/[,₹\s]/g, '');
  if (text === '') return Number.NaN;

  const match = /^([+-]?)(\d*)(?:\.(\d{0,2}))?$/.exec(text);
  if (!match) return Number.NaN;

  const [, sign, whole, fraction = ''] = match;
  if (whole === '' && fraction === '') return Number.NaN;

  const paise = Number(whole || '0') * 100 + Number(fraction.padEnd(2, '0') || '0');
  return sign === '-' ? -paise : paise;
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
