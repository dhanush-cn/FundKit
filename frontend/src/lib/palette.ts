// Engineered by Dhanush C N (github.com/dhanush-cn)
//
// A categorical ramp for allocation charts, drawn entirely from the brand
// palette rather than from a generic chart library's default hues.
//
// The order matters: adjacent entries differ in lightness as well as hue, so
// the segments stay separable in greyscale and for a colourblind reader.
//
// Every entry also has to clear the panel it is drawn on in BOTH themes, and
// the light theme's panel is the lighter of the two — so the ramp bottoms out
// at a mid indigo rather than the deep one it used to end on, which would
// have disappeared into a light-mode panel.
//
// The gain/loss pair is deliberately absent — those two colours mean "up" and
// "down" everywhere else in this app, and reusing them for "HDFC Flexi Cap"
// would make a green slice look like a profit.

export const ALLOCATION_COLORS = [
  '#ffcf3d', // gold
  '#ffffff', // white
  '#6b66d8', // indigo
  '#c9c7f2', // pale lilac
  '#ffe08a', // pale gold
  '#8b86ef', // violet
] as const;

export function allocationColor(index: number): string {
  return ALLOCATION_COLORS[index % ALLOCATION_COLORS.length];
}
