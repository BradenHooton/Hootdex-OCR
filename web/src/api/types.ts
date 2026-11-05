// Mirrors the Go JSON contracts (SCHEMA.md §3 and internal/adapters/inbound/http).

export const CATEGORIES = [
  "BASEBALL",
  "BASKETBALL",
  "FOOTBALL",
  "HOCKEY",
  "GOLF",
  "POKEMON",
  "YUGIOH",
  "MISC_TCG",
] as const;
export type Category = (typeof CATEGORIES)[number];

export const CONDITIONS = ["NM-MT", "NM", "EX-MT", "VG-EX", "POOR"] as const;
export type RawCondition = (typeof CONDITIONS)[number];

export const GRADERS = ["RAW", "PSA", "BGS", "SGC", "CGC"] as const;
export type GradingCompany = (typeof GRADERS)[number];

export type JobStatus =
  | "PENDING"
  | "PREPROCESSING"
  | "EXTRACTING"
  | "MATCHING"
  | "NEEDS_REVIEW"
  | "COMPLETED"
  | "FAILED";

export type InventoryStatus = "IN_REVIEW" | "IN_STOCK" | "LISTED" | "SOLD" | "VAULT_HOLD";

export interface CardExtractionPayload {
  category: Category | "";
  release_year: number;
  manufacturer: string;
  set_name: string;
  card_number: string;
  player_or_character: string;
  team_or_type: string | null;
  parallel_variant: string;
  serial_number: string | null;
  serial_max: number | null;
  is_rookie: boolean;
  is_autograph: boolean;
  is_memorabilia: boolean;
  is_graded: boolean;
  grading_company: GradingCompany;
  grade_value: number | null;
  cert_number: string | null;
  estimated_raw_condition: RawCondition | null;
  centering_ratio_lr: string | null;
  centering_ratio_tb: string | null;
  confidence_score: number;
  extraction_notes: string;
}

export interface ChecklistEntry {
  id: number;
  category: Category;
  release_year: number;
  manufacturer: string;
  set_name: string;
  card_number: string;
  player_or_character: string;
  team_or_type?: string;
  is_rookie_base: boolean;
  known_parallels: string[];
}

export interface ChecklistCandidate {
  entry: ChecklistEntry;
  score: number;
}

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export type ZoneName = "FRONT_HEADER" | "FRONT_FOOTER" | "BACK_HEADER" | "BACK_FOOTER" | "SLAB_FLIP";

export interface ZoneImage {
  zone: ZoneName;
  side: "FRONT" | "BACK";
  bounds: Rect;
  path: string;
}

export interface BorderWidths {
  left: number;
  right: number;
  top: number;
  bottom: number;
}

export interface PreprocessResult {
  card_type: "RAW_CARD" | "GRADED_SLAB";
  canonical_width: number;
  canonical_height: number;
  zones: ZoneImage[];
  centering_ratio_lr?: string;
  centering_ratio_tb?: string;
  border_px?: BorderWidths;
  corner_wear_score?: number;
  corner_wear?: number[];
  estimated_condition?: RawCondition;
}

export interface IngestionJob {
  id: string;
  batch_id: string;
  front_image_path: string;
  back_image_path?: string;
  status: JobStatus;
  extractor_used?: string;
  raw_ocr_front?: string;
  raw_ocr_back?: string;
  confidence_score: number;
  error_message?: string;
  retry_count: number;
  created_at: string;
  updated_at: string;
}

export interface ReviewItem {
  job: IngestionJob;
  payload: CardExtractionPayload;
  preprocess?: PreprocessResult;
  candidates: ChecklistCandidate[];
  images: { front: string; back?: string };
  low_confidence_fields: string[] | null;
}

export interface StorageLocation {
  id: string;
  vault_code: string;
  case_code: string;
  box_code: string;
  row_code: string;
  slot_number: number;
  capacity: number;
  occupied_count: number;
  barcode_label: string;
}

export interface MarketplaceMeta {
  ebay_title?: string;
  tcgplayer_title?: string;
  ebay_category_id?: string;
  ebay_condition_id?: string;
  ebay_card_condition?: string;
  tcgplayer_condition?: string;
  price_source?: string;
  price_estimated?: boolean;
}

export interface InventoryCard {
  id: string;
  sku: string;
  ingestion_job_id?: string;
  category: Category;
  release_year: number;
  manufacturer: string;
  set_name: string;
  card_number: string;
  player_or_character: string;
  team_or_type?: string;
  parallel_variant: string;
  serial_number?: string;
  serial_max?: number;
  is_rookie: boolean;
  is_autograph: boolean;
  is_memorabilia: boolean;
  is_graded: boolean;
  grading_company?: GradingCompany;
  grade_value?: number;
  cert_number?: string;
  estimated_raw_condition?: RawCondition;
  centering_ratio_lr?: string;
  centering_ratio_tb?: string;
  market_price_cents: number;
  acquisition_cost_cents: number;
  asking_price_cents: number;
  player_impact_tier: number;
  hoot_score: number;
  inventory_status: InventoryStatus;
  marketplace_meta: MarketplaceMeta;
  front_image_url: string;
  back_image_url?: string;
  location?: StorageLocation;
  created_at: string;
}

export interface PipelineStats {
  workers: number;
  active_workers: number;
  processed: number;
  failed: number;
  cards_per_minute: number;
}

export type EventType =
  | "job.queued"
  | "job.status"
  | "job.completed"
  | "job.needs_review"
  | "job.failed"
  | "card.approved"
  | "queue.stats";

export interface PipelineEvent {
  type: EventType;
  job_id?: string;
  batch_id?: string;
  status?: JobStatus;
  message?: string;
  confidence?: number;
  sku?: string;
  worker?: number;
  queue?: Partial<Record<JobStatus, number>>;
  stats?: PipelineStats;
  at: string;
}

export interface ApproveInput {
  payload: CardExtractionPayload;
  checklist_id?: number;
  storage_location_id?: string;
  player_impact_tier?: number;
  acquisition_cost_cents?: number;
  asking_price_cents?: number;
}

export interface InventoryFilter {
  category?: Category | "";
  year?: string;
  min_hoot?: string;
  box?: string;
  status?: InventoryStatus | "";
  q?: string;
}
