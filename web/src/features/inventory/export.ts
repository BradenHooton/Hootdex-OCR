import type { InventoryCard } from "../../api/types";

const cell = (v: unknown): string => {
  const s = v === undefined || v === null ? "" : String(v);
  return /[",\n\r]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
};

export function toCSV(headers: string[], rows: unknown[][]): string {
  return [headers, ...rows].map((r) => r.map(cell).join(",")).join("\r\n") + "\r\n";
}

const dollars = (cents: number) => (cents / 100).toFixed(2);
const absoluteURL = (path: string) => new URL(path, window.location.origin).toString();

function features(c: InventoryCard): string {
  const f: string[] = [];
  if (c.is_rookie) f.push("Rookie");
  if (c.is_autograph) f.push("Autograph");
  if (c.is_memorabilia) f.push("Memorabilia");
  if (c.serial_max) f.push("Serial Numbered");
  if (c.parallel_variant && c.parallel_variant !== "Base") f.push("Parallel/Variety");
  return f.join("|");
}

/** eBay Seller Hub Reports bulk upload (File Exchange layout) — one fixed-price listing per card. */
export function ebayCSV(cards: InventoryCard[]): string {
  const headers = [
    "*Action(SiteID=US|Country=US|Currency=USD|Version=1193)",
    "CustomLabel",
    "*Category",
    "*Title",
    "*ConditionID",
    "*StartPrice",
    "*Quantity",
    "*Format",
    "*Duration",
    "PicURL",
    "C:Player/Athlete",
    "C:Year Manufactured",
    "C:Manufacturer",
    "C:Set",
    "C:Card Number",
    "C:Team",
    "C:Parallel/Variety",
    "C:Print Run",
    "C:Features",
    "C:Professional Grader",
    "C:Grade",
    "C:Certification Number",
    "C:Card Condition",
  ];
  const rows = cards.map((c) => {
    const m = c.marketplace_meta;
    const pics = [c.front_image_url, c.back_image_url].filter(Boolean).map((p) => absoluteURL(p as string));
    return [
      "Add",
      c.sku,
      m.ebay_category_id ?? "261328",
      m.ebay_title ?? `${c.release_year} ${c.manufacturer} ${c.set_name} #${c.card_number} ${c.player_or_character}`.slice(0, 80),
      m.ebay_condition_id ?? (c.is_graded ? "2750" : "4000"),
      dollars(c.asking_price_cents || c.market_price_cents),
      1,
      "FixedPrice",
      "GTC",
      pics.join("|"),
      c.player_or_character,
      c.release_year,
      c.manufacturer,
      c.set_name,
      c.card_number,
      c.team_or_type ?? "",
      c.parallel_variant === "Base" ? "" : c.parallel_variant,
      c.serial_max ?? "",
      features(c),
      c.is_graded ? c.grading_company : "",
      c.is_graded ? c.grade_value : "",
      c.cert_number ?? "",
      c.is_graded ? "" : (m.ebay_card_condition ?? ""),
    ];
  });
  return toCSV(headers, rows);
}

const productLine = (c: InventoryCard) =>
  ({ POKEMON: "Pokemon", YUGIOH: "YuGiOh", MISC_TCG: "Other" })[c.category as "POKEMON" | "YUGIOH" | "MISC_TCG"] ?? "Sports Cards";

/** TCGplayer Seller Portal inventory import (TCGplayer Id is matched in the portal). */
export function tcgplayerCSV(cards: InventoryCard[]): string {
  const headers = [
    "TCGplayer Id",
    "Product Line",
    "Set Name",
    "Product Name",
    "Title",
    "Number",
    "Rarity",
    "Condition",
    "TCG Market Price",
    "Add to Quantity",
    "TCG Marketplace Price",
    "Photo URL",
    "SKU",
  ];
  const rows = cards.map((c) => [
    "",
    productLine(c),
    c.set_name,
    c.player_or_character,
    c.marketplace_meta.tcgplayer_title ?? "",
    c.card_number,
    c.parallel_variant === "Base" ? "" : c.parallel_variant,
    c.marketplace_meta.tcgplayer_condition ?? "Near Mint",
    dollars(c.market_price_cents),
    1,
    dollars(c.asking_price_cents || c.market_price_cents),
    absoluteURL(c.front_image_url),
    c.sku,
  ]);
  return toCSV(headers, rows);
}

export function download(filename: string, content: string, type: string) {
  const url = URL.createObjectURL(new Blob([content], { type }));
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}
