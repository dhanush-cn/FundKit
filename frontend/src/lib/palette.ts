// Engineered by Dhanush C N (github.com/dhanush-cn)
//
// A categorical ramp for allocation charts, drawn entirely from the brand
// palette rather than from a generic chart library's default hues.
//
// The order matters: adjacent entries differ in lightness as well as hue, so
// the segments stay separable in greyscale and for a colourblind reader. The
// gain/loss pair is deliberately absent — those two colours mean "up" and
// "down" everywhere else in this app, and reusing them for "HDFC Flexi Cap"
// would make a green slice look like a profit.

export const ALLOCATION_COLORS = [
  '#ffbd00', // gold
  '#ffffff', // white
  '#4b46c9', // indigo
  '#adaae8', // periwinkle
  '#ffd866', // light gold
  '#2e2aa8', // deep indigo
] as const;

export function allocationColor(index: number): string {
  return ALLOCATION_COLORS[index % ALLOCATION_COLORS.length];
}
