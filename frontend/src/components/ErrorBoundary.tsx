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
      <div className="crash-shell">
        <div className="panel crash-panel">
          <h2 className="panel-title">Something went wrong</h2>
          <p className="helper-text">
            The dashboard hit an unexpected error and stopped rendering. The backend services are
            unaffected — this is a UI fault, not an outage.
          </p>
          <div className="error-banner">{error.message}</div>
          <button className="button button-primary" type="button" onClick={this.handleReset}>
            Try again
          </button>
        </div>
      </div>
    );
  }
}
