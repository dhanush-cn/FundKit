// Engineered by Dhanush C N (github.com/dhanush-cn)
//
// Lives in its own file because eslint's react-refresh rule requires a module
// that defines a component to export it — the same reason state/ is split into
// a provider and a context module.
//
// The styles are injected here rather than added to App.css: nothing in this
// file ships with the application build, and the application stylesheet should
// not carry rules that exist only for the hosted demo.

const STYLES = `
.demo-flag {
  position: fixed;
  right: 16px;
  bottom: 16px;
  z-index: 40;
  max-width: min(340px, calc(100vw - 32px));
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 11px 14px;
  border: 1px solid var(--gold);
  border-radius: var(--radius);
  background-color: var(--surface-sunken);
  box-shadow: var(--shadow);
}
.demo-flag-tag {
  flex: none;
  margin-top: 1px;
  padding: 2px 7px;
  border-radius: 4px;
  background-color: var(--gold);
  color: var(--accent-ink);
  font-size: 10px;
  font-weight: 800;
  letter-spacing: 0.08em;
}
.demo-flag-copy {
  margin: 0;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.5;
}
@media (max-width: 560px) {
  .demo-flag { left: 16px; right: 16px; max-width: none; }
}
`;

if (typeof document !== 'undefined' && !document.getElementById('fundkit-demo-flag-styles')) {
  const style = document.createElement('style');
  style.id = 'fundkit-demo-flag-styles';
  style.textContent = STYLES;
  document.head.appendChild(style);
}

/**
 * The honesty label on the hosted build. Every figure on screen is invented,
 * and a fintech dashboard that does not say so is misleading — keep this
 * visible.
 */
export function DemoFlag() {
  return (
    <aside className="demo-flag">
      <span className="demo-flag-tag">DEMO</span>
      <p className="demo-flag-copy">
        Sample data, served from the browser — no gateway or Kafka behind it. Every control works;
        nothing is a real order or holding.
      </p>
    </aside>
  );
}
