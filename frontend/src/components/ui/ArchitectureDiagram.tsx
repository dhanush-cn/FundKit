// Engineered by Dhanush C N (github.com/dhanush-cn)

interface NodeBoxProps {
  x: number;
  y: number;
  w: number;
  h: number;
  title: string;
  sub?: string;
  variant?: 'service' | 'store' | 'client';
}

/**
 * The order write path, drawn as inline SVG.
 *
 * It is hand-positioned rather than generated because the point of the picture
 * is one specific claim — the call is synchronous only as far as Postgres, and
 * everything past the Kafka publish is a consumer — and a layout engine would
 * happily arrange the boxes in a way that hides it. Solid edges are the
 * synchronous path, the dashed edge is the gRPC read, and the two vertical
 * edges off Kafka are the consumers.
 *
 * It carries a real title/desc pair so a screen reader gets the topology as
 * prose, and the prose underneath the diagram says the same thing again for
 * anyone who cannot see it at all.
 */
function NodeBox({ x, y, w, h, title, sub, variant = 'service' }: NodeBoxProps) {
  return (
    <g className={`diagram-node diagram-node-${variant}`}>
      <rect x={x} y={y} width={w} height={h} rx={8} />
      <text x={x + w / 2} y={sub ? y + h / 2 - 3 : y + h / 2 + 4} className="diagram-node-title">
        {title}
      </text>
      {sub ? (
        <text x={x + w / 2} y={y + h / 2 + 14} className="diagram-node-sub">
          {sub}
        </text>
      ) : null}
    </g>
  );
}

export function ArchitectureDiagram() {
  return (
    <div className="diagram-shell">
      <svg viewBox="0 0 730 300" className="diagram" role="img" aria-labelledby="topo-t topo-d">
        <title id="topo-t">FundKit order write path</title>
        <desc id="topo-d">
          The Control Center calls the API gateway over HTTP. The gateway proxies to order-service,
          which claims an idempotency key in Redis, writes the order to Postgres and publishes
          order.created to Kafka. Portfolio-service and notification-service each consume that
          topic independently. Order-service also reads profit and loss from portfolio-service over
          gRPC.
        </desc>

        <defs>
          <marker
            id="arrow"
            viewBox="0 0 10 10"
            refX="9"
            refY="5"
            markerWidth="6"
            markerHeight="6"
            orient="auto-start-reverse"
          >
            <path d="M 0 0 L 10 5 L 0 10 z" fill="var(--line-strong)" />
          </marker>
          <marker
            id="arrow-accent"
            viewBox="0 0 10 10"
            refX="9"
            refY="5"
            markerWidth="6"
            markerHeight="6"
            orient="auto-start-reverse"
          >
            <path d="M 0 0 L 10 5 L 0 10 z" fill="var(--gold)" />
          </marker>
        </defs>

        {/* Synchronous request path */}
        <path className="diagram-edge" d="M150 140 H184" markerEnd="url(#arrow)" />
        <text x={167} y={106} className="diagram-edge-label">
          HTTP
        </text>

        <path className="diagram-edge" d="M340 140 H364" markerEnd="url(#arrow)" />

        <path className="diagram-edge" d="M520 140 H554" markerEnd="url(#arrow)" />
        <text x={537} y={106} className="diagram-edge-label">
          publish
        </text>

        {/* Order-service dependencies */}
        <path className="diagram-edge" d="M445 166 V194 H349 V216" markerEnd="url(#arrow)" />
        <path className="diagram-edge" d="M445 166 V194 H481 V216" markerEnd="url(#arrow)" />

        {/* Kafka consumers */}
        <path className="diagram-edge" d="M635 114 V68" markerEnd="url(#arrow)" />
        <path className="diagram-edge" d="M635 166 V216" markerEnd="url(#arrow)" />

        {/* Asynchronous read path */}
        <path
          className="diagram-edge diagram-edge-async"
          d="M445 114 V38 H554"
          markerEnd="url(#arrow-accent)"
        />
        <text x={500} y={30} className="diagram-edge-label diagram-edge-label-accent">
          gRPC read
        </text>

        <NodeBox x={10} y={114} w={140} h={52} title="Control Center" sub="this dashboard" variant="client" />
        <NodeBox x={190} y={114} w={150} h={52} title="api-gateway" sub="JWT · rate limit" />
        <NodeBox x={370} y={114} w={150} h={52} title="order-service" sub="write path" />
        <NodeBox x={560} y={114} w={150} h={52} title="Kafka" sub="order.created" variant="store" />
        <NodeBox x={560} y={14} w={150} h={48} title="portfolio-service" variant="service" />
        <NodeBox x={560} y={222} w={150} h={48} title="notification-service" variant="service" />
        <NodeBox x={290} y={222} w={118} h={48} title="Redis" sub="idempotency" variant="store" />
        <NodeBox x={422} y={222} w={118} h={48} title="Postgres" sub="orders" variant="store" />
      </svg>
    </div>
  );
}
