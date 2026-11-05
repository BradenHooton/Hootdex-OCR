import { useMemo, useState } from "react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import {
  createColumnHelper,
  flexRender,
  getCoreRowModel,
  getSortedRowModel,
  useReactTable,
  type SortingState,
} from "@tanstack/react-table";
import { ArrowDown, ArrowUp, Download } from "lucide-react";
import { api, formatCents } from "../../api/client";
import { CATEGORIES, type InventoryCard, type InventoryFilter, type InventoryStatus } from "../../api/types";
import { StorageBadge } from "./StorageBadge";
import { download, ebayCSV, tcgplayerCSV } from "./export";

const STATUSES: InventoryStatus[] = ["IN_STOCK", "LISTED", "VAULT_HOLD", "SOLD", "IN_REVIEW"];
const col = createColumnHelper<InventoryCard>();

function HootBadge({ score }: { score: number }) {
  // Tone steps follow the score bands an operator cares about: premium, solid, bulk.
  const tone = score >= 70 ? "border-toploader text-toploader" : score >= 40 ? "border-good/70 text-good" : "border-rule text-muted";
  return <span className={`tnum inline-block w-12 rounded-sm border px-1 text-center font-display font-semibold ${tone}`}>{score.toFixed(1)}</span>;
}

function Flags({ c }: { c: InventoryCard }) {
  const flags = [
    c.is_rookie && "RC",
    c.is_autograph && "Auto",
    c.is_memorabilia && "Patch",
    c.serial_number ? `#${c.serial_number}` : c.serial_max ? `/${c.serial_max}` : null,
    c.parallel_variant !== "Base" && !c.parallel_variant.startsWith("Numbered") ? c.parallel_variant : null,
  ].filter(Boolean) as string[];
  return (
    <span className="flex flex-wrap gap-1">
      {flags.map((f) => (
        <span key={f} className="rounded-sm bg-raised px-1 text-xs text-muted">
          {f}
        </span>
      ))}
    </span>
  );
}

const columns = [
  col.accessor("front_image_url", {
    header: "",
    enableSorting: false,
    cell: (i) => <img src={i.getValue()} alt="" loading="lazy" className="h-12 w-[2.15rem] rounded-[2px] object-cover shadow-[0_0_0_1px_#3A4459]" />,
  }),
  col.accessor("sku", { header: "SKU", cell: (i) => <span className="tnum whitespace-nowrap text-muted">{i.getValue()}</span> }),
  col.accessor((c) => `${c.player_or_character} ${c.set_name}`, {
    id: "card",
    header: "Card",
    cell: (i) => {
      const c = i.row.original;
      return (
        <div className="min-w-[16rem]">
          <div className="font-semibold">
            {c.player_or_character} <span className="tnum font-normal text-muted">#{c.card_number}</span>
          </div>
          <div className="text-xs text-muted">
            {c.release_year} {c.manufacturer} {c.set_name}
          </div>
          <Flags c={c} />
        </div>
      );
    },
  }),
  col.accessor((c) => (c.is_graded ? `${c.grading_company} ${c.grade_value}` : (c.estimated_raw_condition ?? "")), {
    id: "grade",
    header: "Grade",
    cell: (i) => {
      const c = i.row.original;
      return c.is_graded ? (
        <span className="font-semibold">
          {c.grading_company} {c.grade_value}
        </span>
      ) : (
        <span>
          {c.estimated_raw_condition ?? "Raw"}
          {c.centering_ratio_lr && <span className="tnum block text-xs text-muted">{c.centering_ratio_lr} · {c.centering_ratio_tb}</span>}
        </span>
      );
    },
  }),
  col.accessor("market_price_cents", {
    header: "Market",
    cell: (i) => (
      <span className="tnum" title={i.row.original.marketplace_meta.price_estimated ? "Estimated: no live comps found" : i.row.original.marketplace_meta.price_source}>
        {formatCents(i.getValue())}
        {i.row.original.marketplace_meta.price_estimated && <span className="text-muted">*</span>}
      </span>
    ),
  }),
  col.accessor("asking_price_cents", { header: "Asking", cell: (i) => <span className="tnum">{formatCents(i.getValue())}</span> }),
  col.accessor("hoot_score", { header: "Hoot", cell: (i) => <HootBadge score={i.getValue()} /> }),
  col.accessor((c) => c.location?.id ?? "", { id: "location", header: "Location", cell: (i) => <StorageBadge loc={i.row.original.location} /> }),
  col.accessor("inventory_status", { header: "Status", cell: (i) => <span className="text-xs">{i.getValue().replace("_", " ").toLowerCase()}</span> }),
];

export function InventoryPage() {
  const [filter, setFilter] = useState<InventoryFilter>({});
  const [sorting, setSorting] = useState<SortingState>([{ id: "hoot_score", desc: true }]);
  const inventory = useQuery({ queryKey: ["inventory", filter], queryFn: () => api.inventory(filter), placeholderData: keepPreviousData });
  const storage = useQuery({ queryKey: ["storage"], queryFn: api.storage });
  const boxes = useMemo(() => [...new Set((storage.data?.items ?? []).map((l) => l.box_code))].sort(), [storage.data]);
  const rows = inventory.data?.items ?? [];

  const table = useReactTable({
    data: rows,
    columns,
    state: { sorting },
    onSortingChange: setSorting,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
  });
  const totals = useMemo(() => rows.reduce((t, c) => ({ market: t.market + c.market_price_cents, n: t.n + 1 }), { market: 0, n: 0 }), [rows]);
  const update = (k: keyof InventoryFilter, v: string) => setFilter((f) => ({ ...f, [k]: v }));
  const stamp = new Date().toISOString().slice(0, 10);
  const exportRows = () => table.getSortedRowModel().rows.map((r) => r.original);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex flex-wrap items-end gap-3 border-b border-rule px-4 py-3">
        <label className="w-64 text-sm">
          <span className="mb-1 block text-muted">Search</span>
          <input className="field" placeholder="SKU, player, set…" value={filter.q ?? ""} onChange={(e) => update("q", e.target.value)} />
        </label>
        <Select label="Category" value={filter.category ?? ""} onChange={(v) => update("category", v)} options={CATEGORIES.map((c) => [c, c.replace("_", " ").toLowerCase()])} />
        <label className="w-20 text-sm">
          <span className="mb-1 block text-muted">Year</span>
          <input className="field tnum" inputMode="numeric" value={filter.year ?? ""} onChange={(e) => update("year", e.target.value.replace(/\D/g, "").slice(0, 4))} />
        </label>
        <label className="w-24 text-sm">
          <span className="mb-1 block text-muted">Hoot at least</span>
          <input className="field tnum" inputMode="numeric" value={filter.min_hoot ?? ""} onChange={(e) => update("min_hoot", e.target.value.replace(/[^\d.]/g, ""))} />
        </label>
        <Select label="Box" value={filter.box ?? ""} onChange={(v) => update("box", v)} options={boxes.map((b) => [b, b])} />
        <Select label="Status" value={filter.status ?? ""} onChange={(v) => update("status", v)} options={STATUSES.map((s) => [s, s.replace("_", " ").toLowerCase()])} />
        <div className="ml-auto flex flex-wrap gap-2">
          <button type="button" className="btn-quiet" disabled={rows.length === 0} onClick={() => download(`hootdex-ebay-${stamp}.csv`, ebayCSV(exportRows()), "text/csv")}>
            <Download size={14} aria-hidden /> eBay CSV
          </button>
          <button type="button" className="btn-quiet" disabled={rows.length === 0} onClick={() => download(`hootdex-tcgplayer-${stamp}.csv`, tcgplayerCSV(exportRows()), "text/csv")}>
            <Download size={14} aria-hidden /> TCGplayer CSV
          </button>
          <button type="button" className="btn-quiet" disabled={rows.length === 0} onClick={() => download(`hootdex-inventory-${stamp}.json`, JSON.stringify(exportRows(), null, 2), "application/json")}>
            <Download size={14} aria-hidden /> JSON
          </button>
        </div>
      </div>

      <div className="flex items-baseline gap-4 px-4 py-2 text-sm text-muted">
        <span>
          <span className="tnum font-display text-lg font-semibold text-ink">{totals.n}</span> cards
        </span>
        <span>
          <span className="tnum font-display text-lg font-semibold text-ink">{formatCents(totals.market)}</span> market value
        </span>
        {inventory.isFetching && <span>Refreshing…</span>}
        {inventory.isError && <span className="text-bad">Couldn’t load inventory: {inventory.error.message}</span>}
      </div>

      <div className="min-h-0 flex-1 overflow-auto">
        <table className="w-full border-collapse text-sm">
          <thead className="sticky top-0 z-10 bg-panel">
            {table.getHeaderGroups().map((hg) => (
              <tr key={hg.id}>
                {hg.headers.map((h) => {
                  const sorted = h.column.getIsSorted();
                  return (
                    <th key={h.id} scope="col" className="border-b border-rule px-3 py-2 text-left font-display font-semibold" aria-sort={sorted ? (sorted === "asc" ? "ascending" : "descending") : undefined}>
                      {h.column.getCanSort() ? (
                        <button type="button" className="flex items-center gap-1 hover:text-toploader" onClick={h.column.getToggleSortingHandler()}>
                          {flexRender(h.column.columnDef.header, h.getContext())}
                          {sorted === "asc" && <ArrowUp size={12} aria-hidden />}
                          {sorted === "desc" && <ArrowDown size={12} aria-hidden />}
                        </button>
                      ) : (
                        flexRender(h.column.columnDef.header, h.getContext())
                      )}
                    </th>
                  );
                })}
              </tr>
            ))}
          </thead>
          <tbody>
            {table.getRowModel().rows.map((r) => (
              <tr key={r.id} className="border-b border-rule/60 hover:bg-panel">
                {r.getVisibleCells().map((c) => (
                  <td key={c.id} className="px-3 py-1.5 align-middle">
                    {flexRender(c.column.columnDef.cell, c.getContext())}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
        {!inventory.isPending && rows.length === 0 && (
          <p className="p-6 text-muted">No cards match these filters. Clear a filter, or scan cards to start stocking the vault.</p>
        )}
      </div>
    </div>
  );
}

function Select(props: { label: string; value: string; onChange: (v: string) => void; options: [string, string][] }) {
  return (
    <label className="w-36 text-sm">
      <span className="mb-1 block text-muted">{props.label}</span>
      <select className="field capitalize" value={props.value} onChange={(e) => props.onChange(e.target.value)}>
        <option value="">Any</option>
        {props.options.map(([v, l]) => (
          <option key={v} value={v}>
            {l}
          </option>
        ))}
      </select>
    </label>
  );
}
