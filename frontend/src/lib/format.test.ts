// Engineered by Dhanush C N (github.com/dhanush-cn)
import { describe, expect, it } from 'vitest';

import { formatCurrency, formatDate, statusTone } from './format';
import type { OrderStatus } from '../types';

describe('formatCurrency', () => {
  it('renders rupees without fractional noise', () => {
    const formatted = formatCurrency(25118);
    expect(formatted).toContain('25,118');
    expect(formatted).not.toContain('.');
  });

  // A NaN slipping in from a parsed form field should show as zero rather than
  // painting "₹NaN" across the dashboard.
  it('falls back to zero for values that are not finite', () => {
    expect(formatCurrency(Number.NaN)).toContain('0');
    expect(formatCurrency(Number.POSITIVE_INFINITY)).toContain('0');
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
