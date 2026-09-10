// Engineered by Dhanush C N (github.com/dhanush-cn)
import { allocationColor } from '../../lib/palette';

export interface DonutSlice {
  key: string;
  label: string;
  value: number;
}

interface DonutProps {
  slices: DonutSlice[];
  size?: number;
  thickness?: number;
  centerLabel?: string;
  centerValue?: string;
}

/**
 * Allocation ring, drawn as inline SVG rather than pulled from a chart
 * library.
 *
 * A donut is one circle per slice with a dash pattern and an offset, which is
 * about twenty lines — cheaper than the bundle cost of a charting dependency
 * and, more usefully here, it inherits the brand tokens directly instead of
 * being themed around a library's defaults. Every slice also appears in the
 * legend with its percentage, so the chart is never the only way to read the
 * number.
 */
export function Donut({
  slices,
  size = 168,
  thickness = 22,
  centerLabel,
  centerValue,
}: DonutProps) {
  const radius = (size - thickness) / 2;
  const circumference = 2 * Math.PI * radius;
  const total = slices.reduce((sum, slice) => sum + Math.max(slice.value, 0), 0);

  // Running offset in user units, converted to the dash offset each circle
  // needs. Accumulating in a local rather than with reduce keeps the arc maths
  // readable next to the SVG it produces.
  let consumed = 0;

  return (
    <div className="donut">
      <svg
        width={size}
        height={size}
        viewBox={`0 0 ${size} ${size}`}
        role="img"
        aria-label={
          total > 0
            ? `Allocation across ${slices.length} ${slices.length === 1 ? 'fund' : 'funds'}`
            : 'No allocation to display'
        }
      >
        <g transform={`rotate(-90 ${size / 2} ${size / 2})`}>
          <circle
            cx={size / 2}
            cy={size / 2}
            r={radius}
            fill="none"
            stroke="var(--surface-raised)"
            strokeWidth={thickness}
          />
          {total > 0 &&
            slices.map((slice, index) => {
              const share = Math.max(slice.value, 0) / total;
              const length = share * circumference;
              const dashOffset = -consumed;
              consumed += length;

              return (
                <circle
                  key={slice.key}
                  cx={size / 2}
                  cy={size / 2}
                  r={radius}
                  fill="none"
                  stroke={allocationColor(index)}
                  strokeWidth={thickness}
                  // A hair of gap between segments so two adjacent slices read
                  // as two, without a stroke-coloured divider that would have
                  // to know the surface behind it.
                  strokeDasharray={`${Math.max(length - 1.5, 0)} ${circumference}`}
                  strokeDashoffset={dashOffset}
                />
              );
            })}
        </g>
      </svg>

      {(centerLabel || centerValue) && (
        <div className="donut-center">
          {centerValue ? <span className="donut-center-value">{centerValue}</span> : null}
          {centerLabel ? <span className="donut-center-label">{centerLabel}</span> : null}
        </div>
      )}
    </div>
  );
}
