import { useState, type ReactNode } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Check, Search, X } from "lucide-react";
import { api } from "../../api/client";
import {
  CATEGORIES,
  CONDITIONS,
  GRADERS,
  type CardExtractionPayload,
  type ChecklistCandidate,
  type ChecklistEntry,
  type ReviewItem,
} from "../../api/types";
import { modLabel, useHotkeys } from "../../hooks/useHotkeys";

type Key = keyof CardExtractionPayload;

const IDENTITY: Key[] = ["category", "release_year", "manufacturer", "set_name", "card_number", "player_or_character"];

/** The verification form. Amber marks fields the OCR was unsure about until the operator touches them. */
export function ReviewForm(props: { item: ReviewItem; onApproved: (sku: string) => void; onRejected: () => void }) {
  const { item } = props;
  const [p, setP] = useState<CardExtractionPayload>(item.payload);
  const [checklistId, setChecklistId] = useState<number | undefined>();
  const [touched, setTouched] = useState<Set<string>>(new Set());
  const [tier, setTier] = useState(3);
  const [cost, setCost] = useState("");
  const [slot, setSlot] = useState("");
  const [lookup, setLookup] = useState("");
  const [searchTerm, setSearchTerm] = useState("");
  const low = new Set(item.low_confidence_fields ?? []);

  const storage = useQuery({ queryKey: ["storage"], queryFn: api.storage });
  const search = useQuery({
    queryKey: ["checklist-search", searchTerm],
    queryFn: () => api.searchChecklist(searchTerm),
    enabled: searchTerm.length > 1,
  });
  const candidates: ChecklistCandidate[] = searchTerm && search.data ? search.data.items : item.candidates;
  const snapped = candidates.find((c) => c.entry.id === checklistId)?.entry;

  const set = <K extends Key>(k: K, v: CardExtractionPayload[K]) => {
    setP((prev) => ({ ...prev, [k]: v }));
    setTouched((t) => new Set(t).add(k));
    if (IDENTITY.includes(k)) setChecklistId(undefined);
  };

  const snap = (e: ChecklistEntry) => {
    setP((prev) => ({
      ...prev,
      category: e.category,
      release_year: e.release_year,
      manufacturer: e.manufacturer,
      set_name: e.set_name,
      card_number: e.card_number,
      player_or_character: e.player_or_character,
      team_or_type: e.team_or_type || prev.team_or_type,
      is_rookie: prev.is_rookie || e.is_rookie_base,
      parallel_variant: e.known_parallels.includes(prev.parallel_variant) ? prev.parallel_variant : "Base",
    }));
    setChecklistId(e.id);
    setTouched((t) => new Set([...t, ...IDENTITY]));
  };

  const approve = useMutation({
    mutationFn: () =>
      api.approve(item.job.id, {
        payload: p,
        checklist_id: checklistId,
        storage_location_id: slot || undefined,
        player_impact_tier: tier,
        acquisition_cost_cents: cost ? Math.round(Number(cost) * 100) : undefined,
      }),
    onSuccess: (card) => props.onApproved(card.sku),
  });
  const reject = useMutation({
    mutationFn: () => api.reject(item.job.id, "unusable scan"),
    onSuccess: props.onRejected,
  });
  const busy = approve.isPending || reject.isPending;

  // useHotkeys keeps the latest map in a ref, so rebuilding it each render is cheap and never stale.
  const hotkeys: Record<string, () => void> = {
    "mod+enter": () => !busy && approve.mutate(),
    "mod+backspace": () => !busy && reject.mutate(),
  };
  candidates.slice(0, 9).forEach((c, i) => (hotkeys[`${i + 1}`] = () => snap(c.entry)));
  useHotkeys(hotkeys);

  const isLow = (k: string) => low.has(k) && !touched.has(k);
  const parallels = snapped?.known_parallels ?? candidates[0]?.entry.known_parallels ?? [];
  const openSlots = (storage.data?.items ?? []).filter((l) => l.occupied_count < l.capacity);

  return (
    <form
      className="flex min-h-0 flex-col"
      onSubmit={(e) => {
        e.preventDefault();
        approve.mutate();
      }}
    >
      <div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-5 py-4">
        <section aria-labelledby="cands">
          <div className="mb-2 flex items-baseline justify-between">
            <h3 id="cands" className="font-display text-base font-semibold">
              Checklist matches
            </h3>
            <span className="text-xs text-muted">Press 1–9 to snap</span>
          </div>
          <div className="mb-2 flex gap-2">
            <input
              className="field"
              placeholder="Search the checklist, e.g. Stroud 339"
              value={lookup}
              onChange={(e) => setLookup(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  setSearchTerm(lookup.trim());
                }
              }}
              aria-label="Search the master checklist"
            />
            <button type="button" className="btn-quiet px-2" onClick={() => setSearchTerm(lookup.trim())} aria-label="Search">
              <Search size={14} />
            </button>
          </div>
          {candidates.length === 0 ? (
            <p className="text-sm text-muted">No checklist entries match this scan. Search above or fill in the card by hand.</p>
          ) : (
            <ol className="divide-y divide-rule border-y border-rule">
              {candidates.slice(0, 9).map((c, i) => (
                <li key={c.entry.id}>
                  <button
                    type="button"
                    onClick={() => snap(c.entry)}
                    className={`flex w-full items-center gap-3 px-2 py-1.5 text-left text-sm hover:bg-raised ${
                      checklistId === c.entry.id ? "bg-toploader/15" : ""
                    }`}
                  >
                    <span className="kbd tnum">{i + 1}</span>
                    <span className="min-w-0 flex-1 truncate">
                      <span className="text-muted">
                        {c.entry.release_year} {c.entry.manufacturer} {c.entry.set_name}
                      </span>{" "}
                      #{c.entry.card_number} <span className="font-semibold">{c.entry.player_or_character}</span>
                    </span>
                    <ScoreBar score={c.score} />
                    {checklistId === c.entry.id && <Check size={14} className="text-toploader" aria-label="Snapped" />}
                  </button>
                </li>
              ))}
            </ol>
          )}
        </section>

        <section aria-labelledby="identity" className="grid grid-cols-6 gap-3">
          <h3 id="identity" className="col-span-6 font-display text-base font-semibold">
            Card identity
          </h3>
          <Field label="Player / character" low={isLow("player_or_character")} span={6}>
            <input className={fieldClass(isLow("player_or_character"))} value={p.player_or_character} onChange={(e) => set("player_or_character", e.target.value)} />
          </Field>
          <Field label="Year" low={isLow("release_year")} span={2}>
            <input
              className={`${fieldClass(isLow("release_year"))} tnum`}
              inputMode="numeric"
              value={p.release_year || ""}
              onChange={(e) => set("release_year", Number(e.target.value.replace(/\D/g, "")) || 0)}
            />
          </Field>
          <Field label="Category" low={isLow("category")} span={4}>
            <select className={fieldClass(isLow("category"))} value={p.category} onChange={(e) => set("category", e.target.value as CardExtractionPayload["category"])}>
              <option value="">Choose…</option>
              {CATEGORIES.map((c) => (
                <option key={c} value={c}>
                  {c.replace("_", " ").toLowerCase()}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Manufacturer" low={isLow("manufacturer")} span={3}>
            <input className={fieldClass(isLow("manufacturer"))} value={p.manufacturer} onChange={(e) => set("manufacturer", e.target.value)} />
          </Field>
          <Field label="Set" low={isLow("set_name")} span={3}>
            <input className={fieldClass(isLow("set_name"))} value={p.set_name} onChange={(e) => set("set_name", e.target.value)} />
          </Field>
          <Field label="Card number" low={isLow("card_number")} span={2}>
            <input className={`${fieldClass(isLow("card_number"))} tnum`} value={p.card_number} onChange={(e) => set("card_number", e.target.value)} />
          </Field>
          <Field label="Team / type" span={4}>
            <input className="field" value={p.team_or_type ?? ""} onChange={(e) => set("team_or_type", e.target.value || null)} />
          </Field>
          <Field label="Parallel" span={3}>
            <input className="field" list="parallels" value={p.parallel_variant} onChange={(e) => set("parallel_variant", e.target.value)} />
            <datalist id="parallels">
              {parallels.map((v) => (
                <option key={v} value={v} />
              ))}
            </datalist>
          </Field>
          <Field label="Serial (e.g. 045/199)" span={3}>
            <input
              className="field tnum"
              value={p.serial_number ?? ""}
              onChange={(e) => {
                const v = e.target.value.trim();
                set("serial_number", v || null);
                const max = Number(v.split("/")[1]);
                set("serial_max", Number.isFinite(max) && max > 0 ? max : null);
              }}
            />
          </Field>
          <div className="col-span-6 flex flex-wrap gap-x-5 gap-y-2 text-sm">
            <Toggle label="Rookie" checked={p.is_rookie} onChange={(v) => set("is_rookie", v)} />
            <Toggle label="Autograph" checked={p.is_autograph} onChange={(v) => set("is_autograph", v)} />
            <Toggle label="Memorabilia" checked={p.is_memorabilia} onChange={(v) => set("is_memorabilia", v)} />
            <Toggle label="Graded slab" checked={p.is_graded} onChange={(v) => set("is_graded", v)} />
          </div>
        </section>

        <section aria-labelledby="condition" className="grid grid-cols-6 gap-3">
          <h3 id="condition" className="col-span-6 font-display text-base font-semibold">
            Condition and valuation
          </h3>
          {p.is_graded ? (
            <>
              <Field label="Grader" span={2}>
                <select className="field" value={p.grading_company} onChange={(e) => set("grading_company", e.target.value as CardExtractionPayload["grading_company"])}>
                  {GRADERS.filter((g) => g !== "RAW").map((g) => (
                    <option key={g}>{g}</option>
                  ))}
                </select>
              </Field>
              <Field label="Grade" span={2}>
                <input className="field tnum" inputMode="decimal" value={p.grade_value ?? ""} onChange={(e) => set("grade_value", e.target.value ? Number(e.target.value) : null)} />
              </Field>
              <Field label="Cert number" span={2}>
                <input className="field tnum" value={p.cert_number ?? ""} onChange={(e) => set("cert_number", e.target.value || null)} />
              </Field>
            </>
          ) : (
            <Field label="Raw condition" span={2}>
              <select
                className="field"
                value={p.estimated_raw_condition ?? ""}
                onChange={(e) => set("estimated_raw_condition", (e.target.value || null) as CardExtractionPayload["estimated_raw_condition"])}
              >
                <option value="">Unknown</option>
                {CONDITIONS.map((c) => (
                  <option key={c}>{c}</option>
                ))}
              </select>
            </Field>
          )}
          <Field label="Player impact" span={2}>
            <select className="field" value={tier} onChange={(e) => setTier(Number(e.target.value))}>
              <option value={1}>1 · Hall of Fame / superstar</option>
              <option value={2}>2 · Star</option>
              <option value={3}>3 · Starter</option>
              <option value={4}>4 · Role player</option>
              <option value={5}>5 · Common</option>
            </select>
          </Field>
          <Field label="Paid ($)" span={2}>
            <input className="field tnum" inputMode="decimal" placeholder="0.00" value={cost} onChange={(e) => setCost(e.target.value.replace(/[^\d.]/g, ""))} />
          </Field>
          <Field label="Storage slot" span={6}>
            <select className="field" value={slot} onChange={(e) => setSlot(e.target.value)}>
              <option value="">Next open slot</option>
              {openSlots.map((l) => (
                <option key={l.id} value={l.id}>
                  {l.vault_code} › {l.case_code} › {l.box_code} › {l.row_code} › slot {l.slot_number} ({l.capacity - l.occupied_count} free)
                </option>
              ))}
            </select>
          </Field>
        </section>

        <details className="text-sm">
          <summary className="cursor-pointer text-muted hover:text-ink">What the OCR read</summary>
          <pre className="mt-2 max-h-48 overflow-auto whitespace-pre-wrap rounded-sm bg-bench p-2 text-xs text-muted">
            {[item.job.raw_ocr_front, item.job.raw_ocr_back].filter(Boolean).join("\n\n") || "No text was recognized."}
          </pre>
          {item.payload.extraction_notes && <p className="mt-2 text-muted">{item.payload.extraction_notes}</p>}
        </details>
      </div>

      <footer className="flex flex-wrap items-center gap-3 border-t border-rule bg-panel px-5 py-3">
        <button type="submit" className="btn-primary" disabled={busy}>
          <Check size={16} aria-hidden /> {approve.isPending ? "Approving…" : "Approve and next"}
          <span className="kbd border-bench/30 bg-transparent text-bench/80">{modLabel}+Enter</span>
        </button>
        <button type="button" className="btn-quiet" disabled={busy} onClick={() => reject.mutate()}>
          <X size={16} aria-hidden /> Reject scan
          <span className="kbd">{modLabel}+⌫</span>
        </button>
        {(approve.error ?? reject.error) && (
          <p role="alert" className="w-full text-sm text-bad">
            {(approve.error ?? reject.error)?.message}
          </p>
        )}
      </footer>
    </form>
  );
}

const fieldClass = (low: boolean) => (low ? "field field-low" : "field");

function Field(props: { label: string; low?: boolean; span: 2 | 3 | 4 | 6; children: ReactNode }) {
  const span = { 2: "col-span-2", 3: "col-span-3", 4: "col-span-4", 6: "col-span-6" }[props.span];
  return (
    <label className={`${span} block text-sm`}>
      <span className={`mb-1 flex items-center gap-1.5 ${props.low ? "text-amber" : "text-muted"}`}>
        {props.label}
        {props.low && <span className="text-xs">check this</span>}
      </span>
      {props.children}
    </label>
  );
}

function Toggle(props: { label: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="flex items-center gap-2">
      <input type="checkbox" className="accent-toploader" checked={props.checked} onChange={(e) => props.onChange(e.target.checked)} />
      {props.label}
    </label>
  );
}

function ScoreBar({ score }: { score: number }) {
  const pct = Math.round(score * 100);
  const tone = score >= 0.85 ? "bg-good" : score >= 0.6 ? "bg-amber" : "bg-bad";
  return (
    <span className="flex w-20 shrink-0 items-center gap-1.5" title={`Match score ${pct}%`}>
      <span className="h-1.5 flex-1 rounded-full bg-rule">
        <span className={`block h-full rounded-full ${tone}`} style={{ width: `${pct}%` }} />
      </span>
      <span className="tnum w-7 text-right text-xs text-muted">{pct}</span>
    </span>
  );
}
