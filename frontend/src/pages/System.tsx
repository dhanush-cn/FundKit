// FundKit Control Center — system health and topology.
// Engineered by Dhanush C N (github.com/dhanush-cn)
import { AlertTriangle, RefreshCw, Server, ShieldCheck } from 'lucide-react';

import { EmptyState } from '../components/ui/EmptyState';
import { Panel } from '../components/ui/Panel';
import { ArchitectureDiagram } from '../components/ui/ArchitectureDiagram';
import { API_BASE_URL } from '../lib/api';
import { useDashboard } from '../state/dashboard-context';

export function System() {
  const { health } = useDashboard();

  const up = health.services.filter((service) => service.status === 'UP').length;

  return (
    <div className="page">
      <Panel
        kicker="Stack health"
        title={
          health.services.length === 0
            ? 'Probing services'
            : `${up} of ${health.services.length} services up`
        }
        action={
          <button
            type="button"
            className="button button-ghost"
            onClick={() => void health.refresh()}
          >
            <RefreshCw size={14} /> Probe now
          </button>
        }
      >
        {health.error && (
          <div className="error-banner" role="alert">
            {health.error}
          </div>
        )}

        {health.services.length === 0 && !health.error ? (
          <EmptyState
            icon={<Server size={20} />}
            title="No readings yet"
            hint={`Waiting on ${API_BASE_URL}/services/health.`}
          />
        ) : (
          <ul className="service-list">
            {health.services.map((service) => (
              <li
                key={service.service}
                className={`service-item ${service.status === 'UP' ? 'is-up' : 'is-down'}`}
              >
                <span className="service-state" aria-hidden="true" />
                <div className="service-main">
                  <p className="service-name">{service.service}</p>
                  <p className="service-url">{service.url}</p>
                  {service.error ? <p className="service-error">{service.error}</p> : null}
                </div>
                <div className="service-side">
                  <span
                    className={`status-pill ${
                      service.status === 'UP' ? 'status-pill-up' : 'status-pill-down'
                    }`}
                  >
                    {service.status === 'UP' ? (
                      <ShieldCheck size={13} />
                    ) : (
                      <AlertTriangle size={13} />
                    )}
                    {service.status}
                  </span>
                  {service.latency ? (
                    <span className="service-latency">{service.latency}</span>
                  ) : null}
                </div>
              </li>
            ))}
          </ul>
        )}

        <p className="helper-text">
          The gateway probes each service&apos;s <code>/readyz</code> concurrently, not{' '}
          <code>/healthz</code>. Liveness only says a process is running; readiness says its
          dependencies answered — which is the distinction that decides whether Kubernetes should
          send it traffic, and the one this panel reports.
        </p>
      </Panel>

      <Panel kicker="Topology" title="How a single order travels">
        <ArchitectureDiagram />
        <p className="helper-text">
          The write path is synchronous only as far as Postgres. Everything downstream of the Kafka
          publish — valuation, notification — is a consumer that can lag, restart or replay without
          the order-placement call ever knowing. That is what makes the order the system&apos;s
          single source of truth rather than the portfolio balance.
        </p>
      </Panel>

      <Panel kicker="Observability" title="Following one request end to end">
        <ol className="trace-steps">
          <li>
            <p className="trace-title">The gateway mints an id</p>
            <p className="trace-body">
              <code>middleware.RequestID()</code> accepts an inbound <code>x-request-id</code> or
              generates one, then puts it on the response — which is why an error in this UI shows
              a trace id you can grep for directly.
            </p>
          </li>
          <li>
            <p className="trace-title">It rides the gRPC call</p>
            <p className="trace-body">
              The same value is attached as gRPC metadata on the order-service to
              portfolio-service hop, so the P&amp;L read carries the id of the browser request
              that caused it.
            </p>
          </li>
          <li>
            <p className="trace-title">And the Kafka message</p>
            <p className="trace-body">
              It is written as a Kafka header rather than into the payload, so consumers can log it
              without the event schema having to carry a transport concern.
            </p>
          </li>
          <li>
            <p className="trace-title">Every log line is JSON</p>
            <p className="trace-body">
              All four services log structured records, so one id filters the whole causal chain
              across four processes instead of four separate greps you have to line up by
              timestamp.
            </p>
          </li>
        </ol>
      </Panel>
    </div>
  );
}
