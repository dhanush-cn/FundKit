// Engineered by Dhanush C N (github.com/dhanush-cn)
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import type { UseAuth } from '../hooks/useAuth';
import { AuthPanel } from './AuthPanel';

function makeAuth(overrides: Partial<UseAuth> = {}): UseAuth {
  return {
    token: null,
    user: null,
    loading: false,
    error: null,
    clearError: vi.fn(),
    login: vi.fn(async () => true),
    register: vi.fn(async () => true),
    logout: vi.fn(),
    ...overrides,
  };
}

describe('AuthPanel', () => {
  it('opens on the sign-in form', () => {
    render(<AuthPanel auth={makeAuth()} />);

    expect(screen.getByRole('tab', { name: 'Sign in' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByLabelText(/username/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/password/i)).toBeInTheDocument();
  });

  it('submits the typed credentials', async () => {
    const auth = makeAuth();
    render(<AuthPanel auth={auth} />);

    await userEvent.type(screen.getByLabelText(/username/i), 'dhanush');
    await userEvent.type(screen.getByLabelText(/password/i), 'correct-horse-battery');
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }));

    expect(auth.login).toHaveBeenCalledWith({
      username: 'dhanush',
      password: 'correct-horse-battery',
    });
  });

  it('collects the contact details a notification needs when registering', async () => {
    const auth = makeAuth();
    render(<AuthPanel auth={auth} />);

    await userEvent.click(screen.getByRole('tab', { name: 'Create account' }));

    await userEvent.type(screen.getByLabelText(/username/i), 'dhanush');
    await userEvent.type(screen.getByLabelText(/full name/i), 'Dhanush C N');
    await userEvent.type(screen.getByLabelText(/email/i), 'dhanush@example.com');
    await userEvent.type(screen.getByLabelText(/phone/i), '+919876543210');
    await userEvent.type(screen.getByLabelText(/password/i), 'correct-horse-battery');
    await userEvent.click(screen.getByRole('button', { name: 'Create account' }));

    // Email and phone are the point: without them an execution alert has
    // nowhere to go.
    expect(auth.register).toHaveBeenCalledWith({
      username: 'dhanush',
      full_name: 'Dhanush C N',
      email: 'dhanush@example.com',
      phone: '+919876543210',
      password: 'correct-horse-battery',
    });
  });

  it('clears a stale error when switching between the forms', async () => {
    const auth = makeAuth({ error: 'invalid username or password' });
    render(<AuthPanel auth={auth} />);

    expect(screen.getByRole('alert')).toHaveTextContent('invalid username or password');

    await userEvent.click(screen.getByRole('tab', { name: 'Create account' }));
    expect(auth.clearError).toHaveBeenCalled();
  });

  it('disables the submit button while a request is in flight', () => {
    render(<AuthPanel auth={makeAuth({ loading: true })} />);

    expect(screen.getByRole('button', { name: /signing in/i })).toBeDisabled();
  });

  it('never renders a password in plain text', async () => {
    render(<AuthPanel auth={makeAuth()} />);

    const password = screen.getByLabelText(/password/i);
    expect(password).toHaveAttribute('type', 'password');

    await userEvent.click(screen.getByRole('tab', { name: 'Create account' }));
    expect(screen.getByLabelText(/password/i)).toHaveAttribute('type', 'password');
  });
});
