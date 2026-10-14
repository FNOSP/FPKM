import React from 'react';
import { createRoot } from 'react-dom/client';
import '@semi-css';
import './styles/global.css';
import App from './App';
import ErrorBoundary from './components/ErrorBoundary';

const container = document.getElementById('root');
if (container) {
  createRoot(container).render(
    <React.StrictMode>
      <ErrorBoundary>
        <App />
      </ErrorBoundary>
    </React.StrictMode>,
  );
}
