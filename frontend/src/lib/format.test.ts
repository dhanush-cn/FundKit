// Engineered by Dhanush C N (github.com/dhanush-cn)
import { describe, expect, it } from 'vitest';

import { formatCurrency, formatDate, formatPaise, paiseToRupees, rupeesToPaise, statusTone } from './format';
import type { OrderStatus } from '../types';

describe('formatCurrency', () => {
  // Rupee input — the unit portfolio-service reports over gRPC.
  it('renders rupees to paise precision', () => {
    const formatted = formatCurrency(25118.5);
    expect(formatted).toContain('25,118.50');
  });

  // A NaN slipping in from a parsed form field should show as zero rather than
  // painting "₹NaN" across the dashboard.
  it('falls back to zero for values that are not finite', () => {
    expect(formatCurrency(Number.NaN)).toContain('0');
    expect(formatCurrency(Number.POSITIVE_INFINITY)).toContain('0');
  });
});

describe('formatPaise', () => {
  // Paise input — the unit order-service reports. The 100x difference between
  // this and formatCurrency is the whole reason they are separate functions.
  it('renders integer paise as rupees', () => {
    expect(formatPaise(10050)).toContain('100.50');
    expect(formatPaise(2511850)).toContain('25,118.50');
  });

  it('groups in the Indian convention', () => {
    // 12,34,567.89 — lakhs, not millions.
    expect(formatPaise(123456789)).toContain('12,34,567.89');
  });

  it('renders zero rather than a stray symbol', () => {
    expect(formatPaise(0)).toContain('0.00');
  });
});

describe('rupeesToPaise', () => {
  it('converts a plain rupee figure', () => {
    expect(rupeesToPaise('5000')).toBe(500000);
    expect(rupeesToPaise('100.50')).toBe(10050);
    expect(rupeesToPaise('0.01')).toBe(1);
  });

  // The reason the parser works on the decimal text instead of parseFloat:
  // 8.115 * 100 is 811.4999999999999 in IEEE-754 and rounds to the wrong paise.
  it('is exact where a float multiplication is not', () => {
    expect(rupeesToPaise('8.11')).toBe(811);
    expect(rupeesToPaise('1.10')).toBe(110);
    expect(rupeesToPaise('0.07')).toBe(7);
    expect(rupeesToPaise('70.07')).toBe(7007);
  });

  it('tolerates the symbols and separators a person types', () => {
    expect(rupeesToPaise('₹1,500.50')).toBe(150050);
    expect(rupeesToPaise(' 1500 ')).toBe(150000);
  });

  it('pads a single decimal place', () => {
    expect(rupeesToPaise('10.5')).toBe(1050);
  });

  // NaN rather than 0, so the caller refuses to submit instead of quietly
  // placing an order for nothing.
  it('rejects what is not an amount', () => {
    expect(rupeesToPaise('')).toBeNaN();
    expect(rupeesToPaise('abc')).toBeNaN();
    expect(rupeesToPaise('10.005')).toBeNaN();
  });
});

describe('paiseToRupees', () => {
  it('round-trips with rupeesToPaise', () => {
    expect(paiseToRupees(rupeesToPaise('100.50'))).toBe(100.5);
  });
});

describe('formatDate', () => {
  it('renders a valid timestamp', () => {
    const formatted = formatDate('2026-09-02T10:30:00Z');
    expect(formatted).not.toBe('2026-09-02T10:30:00Z');
    expect(formatted).toMatch(/2026/);
  });

  it('returns the original string when the value cannot be parsed', () => {
    expect(formatDate('not-a-date')).toBe('not-a-date');
  });
});

describe('statusTone', () => {
  it('maps every lifecycle state to its own tone', () => {
    const tones: Record<OrderStatus, string> = {
      PENDING: 'pending',
      PROCESSING: 'processing',
      EXECUTED: 'executed',
      FAILED: 'failed',
    };

    for (const [status, tone] of Object.entries(tones)) {
      expect(statusTone(status as OrderStatus)).toBe(tone);
    }
  });

  it('degrades to the neutral tone for an unrecognised status', () => {
    expect(statusTone('SOMETHING_NEW' as OrderStatus)).toBe('pending');
  });
});
