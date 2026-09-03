// Test bootstrap: jest-dom matchers, and a clean slate between tests.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterEach, beforeEach, vi } from 'vitest';

beforeEach(() => {
  // Every test starts signed out. A leaked token from a previous test would
  // silently change what the next one renders.
  localStorage.clear();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
