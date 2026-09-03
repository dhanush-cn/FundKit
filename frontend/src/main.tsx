// FundKit Control Center — application entry point.
// Engineered by Dhanush C N (github.com/dhanush-cn)
import React from 'react';
import ReactDOM from 'react-dom/client';

import App from './App.tsx';
import { ErrorBoundary } from './components/ErrorBoundary.tsx';
import './index.css';

const container = document.getElementById('root');
if (!container) {
  throw new Error('FundKit Control Center: #root element is missing from index.html');
}

ReactDOM.createRoot(container).render(
  <React.StrictMode>
    <ErrorBoundary>
      <App />
    </ErrorBoundary>
  </React.StrictMode>,
);
