import { Activity, Boxes, ScanLine, Upload } from "lucide-react";
import type { ReactNode } from "react";
import type { PipelineEvent } from "../api/types";
import type { StreamState } from "../hooks/useJobStream";

export type View = "review" | "inventory" | "upload";

function describe(ev: PipelineEvent): string {
  switch (ev.type) {
    case "job.completed":
      return `Stocked ${ev.sku ?? ""} → ${ev.message ?? "slot"}`;
    case "card.approved":
      return `Approved ${ev.sku ?? ""} → ${ev.message ?? "slot"}`;
    case "job.needs_review":
      return `Sent to review: ${ev.message ?? ""}`;
    case "job.failed":
      return `Scan failed: ${ev.message ?? ""}`;
    case "job.queued":
      return `Scan received from batch ${ev.batch_id ?? ""}`;
    default:
      return ev.status ? `${ev.status.toLowerCase()} ${ev.message ?? ""}` : (ev.message ?? "");
  }
}

function Tab(props: { active: boolean; onClick: () => void; children: ReactNode; hotkey: string }) {
  return (
    <button
      type="button"
      onClick={props.onClick}
      aria-current={props.active ? "page" : undefined}
      title={`Alt+${props.hotkey}`}
      className={`flex items-center gap-2 border-b-2 px-3 py-3 font-display text-base font-semibold tracking-wide transition-colors ${
        props.active ? "border-toploader text-ink" : "border-transparent text-muted hover:text-ink"
      }`}
    >
      {props.children}
    </button>
  );
}

export function StatusBar(props: { stream: StreamState; view: View; onNavigate: (v: View) => void }) {
  const { queue, stats, recent, connected } = props.stream;
  const inFlight = (queue.PENDING ?? 0) + (queue.PREPROCESSING ?? 0) + (queue.EXTRACTING ?? 0) + (queue.MATCHING ?? 0);
  const review = queue.NEEDS_REVIEW ?? 0;
  const workers = stats?.workers ?? 0;
  const active = stats?.active_workers ?? 0;
  const last = recent[0];

  return (
    <header className="flex flex-wrap items-stretch gap-x-6 border-b border-rule bg-panel px-4">
      <div className="flex items-center gap-2 py-2">
        <span className="font-display text-xl font-bold tracking-tight">Hootdex</span>
      </div>
      <nav className="flex items-stretch" aria-label="Workstation">
        <Tab active={props.view === "review"} onClick={() => props.onNavigate("review")} hotkey="1">
          <ScanLine size={16} aria-hidden /> Review
          {review > 0 && <span className="tnum rounded-sm bg-amber px-1.5 text-sm text-bench">{review}</span>}
        </Tab>
        <Tab active={props.view === "inventory"} onClick={() => props.onNavigate("inventory")} hotkey="2">
          <Boxes size={16} aria-hidden /> Inventory
        </Tab>
        <Tab active={props.view === "upload"} onClick={() => props.onNavigate("upload")} hotkey="3">
          <Upload size={16} aria-hidden /> Upload
        </Tab>
      </nav>

      <div className="ml-auto flex flex-wrap items-center gap-x-6 gap-y-1 py-2 text-sm" aria-live="polite">
        <span className="flex items-center gap-2" title={connected ? "Scanner feed connected" : "Reconnecting to the scanner feed"}>
          <span className={`h-2 w-2 rounded-full ${connected ? "bg-good" : "bg-bad animate-pulse"}`} aria-hidden />
          <span className="text-muted">{connected ? "Live" : "Reconnecting"}</span>
        </span>
        <Metric label="in queue" value={inFlight} />
        <span className="flex items-center gap-2">
          <span className="flex gap-1" aria-hidden>
            {Array.from({ length: workers }, (_, i) => (
              <span key={i} className={`h-3 w-1.5 rounded-[1px] ${i < active ? "bg-toploader" : "bg-rule"}`} />
            ))}
          </span>
          <span className="tnum">
            {active}/{workers}
          </span>
          <span className="text-muted">workers busy</span>
        </span>
        <Metric label="cards/min" value={stats ? stats.cards_per_minute.toFixed(1) : "–"} />
        {(stats?.failed ?? 0) > 0 && <Metric label="failed" value={stats?.failed ?? 0} tone="text-bad" />}
        <span className="hidden max-w-[28rem] items-center gap-2 truncate text-muted xl:flex">
          <Activity size={14} aria-hidden />
          <span className="truncate">{last ? describe(last) : "Waiting for the first scan"}</span>
        </span>
      </div>
    </header>
  );
}

function Metric(props: { label: string; value: number | string; tone?: string }) {
  return (
    <span className="flex items-baseline gap-1.5">
      <span className={`tnum font-display text-lg font-semibold ${props.tone ?? ""}`}>{props.value}</span>
      <span className="text-muted">{props.label}</span>
    </span>
  );
}
