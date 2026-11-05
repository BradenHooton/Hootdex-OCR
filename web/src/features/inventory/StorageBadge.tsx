import type { StorageLocation } from "../../api/types";

const short = (code: string) => code.replace(/^(VAULT|CASE|BOX|ROW|SHELF|SHOWCASE)_?/, "");

/** Vault → Case → Box → Row → Slot as one segmented chip; the full path is in the tooltip. */
export function StorageBadge({ loc }: { loc?: StorageLocation }) {
  if (!loc) return <span className="text-muted">Unassigned</span>;
  const tiers: [string, string][] = [
    ["Vault", short(loc.vault_code)],
    ["Case", short(loc.case_code)],
    ["Box", short(loc.box_code)],
    ["Row", short(loc.row_code)],
    ["Slot", String(loc.slot_number)],
  ];
  const full = `${loc.vault_code} › ${loc.case_code} › ${loc.box_code} › ${loc.row_code} › slot ${loc.slot_number}`;
  return (
    <span className="inline-flex overflow-hidden rounded-sm border border-rule text-xs" title={full} aria-label={full}>
      {tiers.map(([tier, value], i) => (
        <span key={tier} className={`tnum px-1.5 py-0.5 ${i === tiers.length - 1 ? "bg-toploader/20 font-semibold text-ink" : "border-r border-rule text-muted"}`}>
          <span className="sr-only">{tier} </span>
          {value}
        </span>
      ))}
    </span>
  );
}
