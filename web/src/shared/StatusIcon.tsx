import type { StatusShape } from "./status";

interface StatusIconProps {
  shape: StatusShape;
  glyph: string;
  /** Fill colour for the shape (a status -fg or white). */
  color: string;
  size?: number;
  /** Glyph colour; defaults to the shape's contrast (white on solid, fg on outline use). */
  glyphColor?: string;
  /** When true the shape is filled solid (chip style); else it is a tinted outline glyph. */
  filled?: boolean;
}

// Status shape glyph. The SHAPE (circle/diamond/triangle) is the non-colour identity cue, so the three
// statuses are distinguishable in greyscale and for colour-vision-deficient users (DESIGN §1.2).
// Decorative: the meaning is always carried alongside by a text label, so aria-hidden.
export function StatusIcon({
  shape,
  glyph,
  color,
  size = 16,
  glyphColor,
  filled = true,
}: StatusIconProps) {
  const stroke = filled ? "none" : color;
  const fill = filled ? color : "transparent";
  const text = glyphColor ?? (filled ? "#ffffff" : color);

  let shapeEl;
  switch (shape) {
    case "circle":
      shapeEl = (
        <circle cx="12" cy="12" r="10" fill={fill} stroke={stroke} strokeWidth={filled ? 0 : 2} />
      );
      break;
    case "diamond":
      shapeEl = (
        <path
          d="M12 1.5 22.5 12 12 22.5 1.5 12Z"
          fill={fill}
          stroke={stroke}
          strokeWidth={filled ? 0 : 2}
          strokeLinejoin="round"
        />
      );
      break;
    case "triangle":
      shapeEl = (
        <path
          d="M12 2 23 21H1Z"
          fill={fill}
          stroke={stroke}
          strokeWidth={filled ? 0 : 2}
          strokeLinejoin="round"
        />
      );
      break;
  }

  // Triangle glyph sits a touch lower (visual centre of a triangle is below geometric centre).
  const glyphY = shape === "triangle" ? 18 : 12;

  return (
    <svg width={size} height={size} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      {shapeEl}
      <text
        x="12"
        y={glyphY}
        textAnchor="middle"
        dominantBaseline="central"
        fontSize="13"
        fontWeight="700"
        fill={text}
        fontFamily="var(--hv-font-family)"
      >
        {glyph}
      </text>
    </svg>
  );
}
