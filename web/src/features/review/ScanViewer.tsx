import { useRef, useState, type PointerEvent, type WheelEvent } from "react";
import { Minus, Plus, RotateCcw } from "lucide-react";
import type { PreprocessResult, ReviewItem, ZoneName } from "../../api/types";
import { useHotkeys } from "../../hooks/useHotkeys";

const ZONE_LABEL: Record<ZoneName, string> = {
  FRONT_HEADER: "Header",
  FRONT_FOOTER: "Footer",
  BACK_HEADER: "Number / bio",
  BACK_FOOTER: "Copyright line",
  SLAB_FLIP: "Grading label",
};

const MIN_ZOOM = 1;
const MAX_ZOOM = 6;

/** Light-box for the cropped card faces: zoom, pan, OCR zone outlines and centering guides. */
export function ScanViewer({ item }: { item: ReviewItem }) {
  const hasBack = Boolean(item.images.back);
  const [side, setSide] = useState<"front" | "back">("front");
  const [zoom, setZoom] = useState(1);
  const [pan, setPan] = useState({ x: 0, y: 0 });
  const [overlay, setOverlay] = useState(true);
  const drag = useRef<{ x: number; y: number; px: number; py: number } | null>(null);
  const pre = item.preprocess;

  const setZoomClamped = (z: number) => {
    const next = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, z));
    setZoom(next);
    if (next === 1) setPan({ x: 0, y: 0 });
  };
  const reset = () => {
    setZoom(1);
    setPan({ x: 0, y: 0 });
  };

  useHotkeys({
    f: () => setSide("front"),
    b: () => hasBack && setSide("back"),
    "=": () => setZoomClamped(zoom * 1.5),
    "+": () => setZoomClamped(zoom * 1.5),
    "-": () => setZoomClamped(zoom / 1.5),
    "0": reset,
    o: () => setOverlay((v) => !v),
  });

  const onWheel = (e: WheelEvent) => setZoomClamped(zoom * (e.deltaY < 0 ? 1.15 : 1 / 1.15));
  const onPointerDown = (e: PointerEvent) => {
    if (zoom === 1) return;
    (e.target as Element).setPointerCapture(e.pointerId);
    drag.current = { x: e.clientX, y: e.clientY, px: pan.x, py: pan.y };
  };
  const onPointerMove = (e: PointerEvent) => {
    if (!drag.current) return;
    setPan({ x: drag.current.px + (e.clientX - drag.current.x) / zoom, y: drag.current.py + (e.clientY - drag.current.y) / zoom });
  };
  const src = side === "front" ? item.images.front : (item.images.back ?? item.images.front);

  return (
    <section className="flex min-h-0 flex-col" aria-label="Card scan">
      <div className="flex items-center gap-2 border-b border-rule px-4 py-2 text-sm">
        <div role="tablist" aria-label="Card side" className="flex overflow-hidden rounded-sm border border-rule">
          {(["front", "back"] as const).map((s) => (
            <button
              key={s}
              role="tab"
              type="button"
              aria-selected={side === s}
              disabled={s === "back" && !hasBack}
              onClick={() => setSide(s)}
              className={`px-3 py-1 capitalize disabled:opacity-40 ${side === s ? "bg-raised text-ink" : "text-muted hover:text-ink"}`}
            >
              {s} <span className="kbd ml-1">{s[0]?.toUpperCase()}</span>
            </button>
          ))}
        </div>
        <label className="ml-2 flex items-center gap-2 text-muted">
          <input type="checkbox" checked={overlay} onChange={(e) => setOverlay(e.target.checked)} className="accent-toploader" />
          OCR zones <span className="kbd">O</span>
        </label>
        <div className="ml-auto flex items-center gap-1">
          <button type="button" className="btn-quiet px-2" onClick={() => setZoomClamped(zoom / 1.5)} aria-label="Zoom out">
            <Minus size={14} />
          </button>
          <span className="tnum w-12 text-center">{Math.round(zoom * 100)}%</span>
          <button type="button" className="btn-quiet px-2" onClick={() => setZoomClamped(zoom * 1.5)} aria-label="Zoom in">
            <Plus size={14} />
          </button>
          <button type="button" className="btn-quiet px-2" onClick={reset} aria-label="Reset zoom">
            <RotateCcw size={14} />
          </button>
        </div>
      </div>

      <div
        className={`relative min-h-0 flex-1 overflow-hidden bg-[#141925] ${zoom > 1 ? "cursor-grab active:cursor-grabbing" : ""}`}
        onWheel={onWheel}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={() => (drag.current = null)}
      >
        <div
          className="absolute inset-0 flex items-center justify-center p-6"
          style={{ transform: `scale(${zoom}) translate(${pan.x}px, ${pan.y}px)`, transformOrigin: "center" }}
        >
          <div className="relative h-full max-h-full" style={{ aspectRatio: pre ? `${pre.canonical_width} / ${pre.canonical_height}` : "5 / 7" }}>
            <img src={src} alt={`${side} of the scanned card`} draggable={false} className="h-full w-full select-none rounded-[3px] shadow-[0_0_0_1px_#3A4459]" />
            {overlay && pre && <Overlay pre={pre} side={side} />}
          </div>
        </div>
      </div>

      {pre && <Metrics pre={pre} />}
    </section>
  );
}

function Overlay({ pre, side }: { pre: PreprocessResult; side: "front" | "back" }) {
  const w = pre.canonical_width;
  const h = pre.canonical_height;
  const zones = pre.zones.filter((z) => z.side.toLowerCase() === side);
  const b = side === "front" ? pre.border_px : undefined;
  return (
    <svg viewBox={`0 0 ${w} ${h}`} className="pointer-events-none absolute inset-0 h-full w-full" aria-hidden>
      {zones.map((z) => (
        <g key={z.zone}>
          <rect x={z.bounds.x + 2} y={z.bounds.y + 2} width={z.bounds.w - 4} height={z.bounds.h - 4} fill="#6FA8FF14" stroke="#6FA8FF" strokeWidth={3} strokeDasharray="12 8" />
          <text x={z.bounds.x + 12} y={z.bounds.y + 30} fill="#E7EAF0" fontSize={22} fontFamily="Barlow Semi Condensed" fontWeight={600} style={{ paintOrder: "stroke" }} stroke="#1B2130" strokeWidth={5}>
            {ZONE_LABEL[z.zone]}
          </text>
        </g>
      ))}
      {b && (
        <g stroke="#F2B33D" strokeWidth={2.5}>
          <line x1={b.left} y1={0} x2={b.left} y2={h} />
          <line x1={w - b.right} y1={0} x2={w - b.right} y2={h} />
          <line x1={0} y1={b.top} x2={w} y2={b.top} />
          <line x1={0} y1={h - b.bottom} x2={w} y2={h - b.bottom} />
        </g>
      )}
    </svg>
  );
}

const CORNERS = ["Top left", "Top right", "Bottom right", "Bottom left"];

function Metrics({ pre }: { pre: PreprocessResult }) {
  return (
    <dl className="grid grid-cols-2 gap-x-6 gap-y-1 border-t border-rule px-4 py-3 text-sm sm:grid-cols-4">
      <Stat term="Format" value={pre.card_type === "GRADED_SLAB" ? "Graded slab" : "Raw card"} />
      <Stat term="Centering L/R" value={pre.centering_ratio_lr ?? "—"} />
      <Stat term="Centering T/B" value={pre.centering_ratio_tb ?? "—"} />
      <Stat term="Condition estimate" value={pre.estimated_condition ?? "—"} />
      {pre.corner_wear && (
        <div className="col-span-full flex flex-wrap items-center gap-x-4 gap-y-1">
          <dt className="text-muted">Corner whitening</dt>
          {pre.corner_wear.map((v, i) => (
            <dd key={CORNERS[i]} className="flex items-center gap-1.5" title={CORNERS[i]}>
              <span className="text-muted">{CORNERS[i]}</span>
              <span className={`tnum ${v > 0.15 ? "text-amber" : ""}`}>{Math.round(v * 100)}%</span>
            </dd>
          ))}
        </div>
      )}
    </dl>
  );
}

function Stat({ term, value }: { term: string; value: string }) {
  return (
    <div>
      <dt className="text-muted">{term}</dt>
      <dd className="tnum font-display text-lg font-semibold">{value}</dd>
    </div>
  );
}
