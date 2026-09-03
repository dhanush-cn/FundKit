// FundKit Control Center — credential screen.
//
// The dashboard is useless without an identity: an order has to belong to
// someone, and a notification has to reach an inbox. This panel is where that
// identity is established.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
import { useCallback, useState } from 'react';
import type { FormEvent } from 'react';
import { motion } from 'framer-motion';
import { Sparkles } from 'lucide-react';

import type { UseAuth } from '../hooks/useAuth';

const AUTHOR = 'Dhanush C N';
const AUTHOR_URL = 'https://github.com/dhanush-cn';

type Mode = 'login' | 'register';

interface AuthPanelProps {
  auth: UseAuth;
}

const emptyRegistration = {
  username: '',
  full_name: '',
  email: '',
  phone: '',
  password: '',
};

export function AuthPanel({ auth }: AuthPanelProps) {
  const [mode, setMode] = useState<Mode>('login');
  const [credentials, setCredentials] = useState({ username: '', password: '' });
  const [registration, setRegistration] = useState(emptyRegistration);

  const switchMode = useCallback(
    (next: Mode) => {
      setMode(next);
      auth.clearError();
    },
    [auth],
  );

  const handleLogin = useCallback(
    async (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault();
      await auth.login(credentials);
    },
    [auth, credentials],
  );

  const handleRegister = useCallback(
    async (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault();
      await auth.register(registration);
    },
    [auth, registration],
  );

  return (
    <div className="app-shell auth-shell">
      <motion.div
        className="panel auth-panel"
        initial={{ opacity: 0, scale: 0.96 }}
        animate={{ opacity: 1, scale: 1 }}
      >
        <div className="brand-mark auth-brand">
          <div className="brand-orb">
            <Sparkles size={24} />
          </div>
          <div>
            <div className="brand-name" style={{ fontSize: 24 }}>
              FundKit
            </div>
            <div className="brand-subtitle">Control Center</div>
          </div>
        </div>

        <div className="auth-tabs" role="tablist">
          <button
            type="button"
            role="tab"
            aria-selected={mode === 'login'}
            className={mode === 'login' ? 'auth-tab auth-tab-active' : 'auth-tab'}
            onClick={() => switchMode('login')}
          >
            Sign in
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={mode === 'register'}
            className={mode === 'register' ? 'auth-tab auth-tab-active' : 'auth-tab'}
            onClick={() => switchMode('register')}
          >
            Create account
          </button>
        </div>

        {mode === 'login' ? (
          <form className="order-form auth-form" onSubmit={(event) => void handleLogin(event)}>
            <label className="full-width">
              Username
              <input
                name="username"
                autoComplete="username"
                value={credentials.username}
                onChange={(event) =>
                  setCredentials({ ...credentials, username: event.target.value })
                }
                required
              />
            </label>
            <label className="full-width">
              Password
              <input
                name="password"
                type="password"
                autoComplete="current-password"
                value={credentials.password}
                onChange={(event) =>
                  setCredentials({ ...credentials, password: event.target.value })
                }
                required
              />
            </label>
            <button className="submit-button" type="submit" disabled={auth.loading}>
              {auth.loading ? 'Signing in…' : 'Sign in'}
            </button>
          </form>
        ) : (
          <form className="order-form auth-form" onSubmit={(event) => void handleRegister(event)}>
            <label>
              Username
              <input
                name="username"
                autoComplete="username"
                value={registration.username}
                onChange={(event) =>
                  setRegistration({ ...registration, username: event.target.value })
                }
                required
              />
            </label>
            <label>
              Full name
              <input
                name="full_name"
                autoComplete="name"
                value={registration.full_name}
                onChange={(event) =>
                  setRegistration({ ...registration, full_name: event.target.value })
                }
                required
              />
            </label>
            <label className="full-width">
              Email
              <input
                name="email"
                type="email"
                autoComplete="email"
                value={registration.email}
                onChange={(event) => setRegistration({ ...registration, email: event.target.value })}
                required
              />
            </label>
            <label className="full-width">
              Phone
              <input
                name="phone"
                type="tel"
                autoComplete="tel"
                placeholder="+919876543210"
                value={registration.phone}
                onChange={(event) => setRegistration({ ...registration, phone: event.target.value })}
                required
              />
            </label>
            <label className="full-width">
              Password
              <input
                name="password"
                type="password"
                autoComplete="new-password"
                minLength={8}
                value={registration.password}
                onChange={(event) =>
                  setRegistration({ ...registration, password: event.target.value })
                }
                required
              />
            </label>
            <button className="submit-button" type="submit" disabled={auth.loading}>
              {auth.loading ? 'Creating account…' : 'Create account'}
            </button>
          </form>
        )}

        {auth.error && (
          <div className="error-banner" role="alert" style={{ marginTop: 16 }}>
            {auth.error}
          </div>
        )}

        <div className="helper-text auth-note">
          {mode === 'login'
            ? 'The gateway verifies your password against a bcrypt hash and returns a signed, expiring token.'
            : 'Your email and phone are stored with the account and travel with every order, so execution alerts reach you rather than a placeholder address.'}
        </div>

        <div className="author-credit" style={{ marginTop: 20, textAlign: 'center' }}>
          Engineered by{' '}
          <a href={AUTHOR_URL} target="_blank" rel="noreferrer">
            {AUTHOR}
          </a>
        </div>
      </motion.div>
    </div>
  );
}
