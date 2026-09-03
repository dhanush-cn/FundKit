// Engineered by Dhanush C N (github.com/dhanush-cn)
import { Component } from 'react';
import type { ErrorInfo, ReactNode } from 'react';

interface Props {
  children: ReactNode;
  fallback?: ReactNode;
}

interface State {
  error: Error | null;
}

/**
 * ErrorBoundary keeps a render-time exception in one subtree from blanking the
 * whole dashboard. React only supports this as a class component, which is why
 * this is the one class in the codebase.
 */
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    console.error('FundKit UI crashed', error, info.componentStack);
  }

  private handleReset = (): void => {
    this.setState({ error: null });
  };

  render(): ReactNode {
    const { error } = this.state;
    if (!error) {
      return this.props.children;
    }
    if (this.props.fallback) {
      return this.props.fallback;
    }

    return (
      <div className="app-shell" style={{ justifyContent: 'center', alignItems: 'center' }}>
        <div className="panel" style={{ maxWidth: 520, margin: '0 auto' }}>
          <h2>Something went wrong</h2>
          <p style={{ color: 'var(--text-secondary)' }}>
            The dashboard hit an unexpected error and stopped rendering. The backend services are
            unaffected.
          </p>
          <div className="error-banner" style={{ marginBottom: 16 }}>{error.message}</div>
          <button className="submit-button" type="button" onClick={this.handleReset}>
            Try again
          </button>
        </div>
      </div>
    );
  }
}
