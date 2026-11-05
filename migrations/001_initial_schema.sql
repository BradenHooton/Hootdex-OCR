-- 1. 5-Tier Physical Storage Hierarchy (Vault -> Case -> Box -> Row -> Slot)
CREATE TABLE IF NOT EXISTS storage_locations (
    id TEXT PRIMARY KEY, -- UUIDv4 or deterministic slug e.g., "V1-C2-B04-R2-S015"
    vault_code TEXT NOT NULL,      -- Tier 1: e.g., "VAULT_A", "SHOWCASE_1"
    case_code TEXT NOT NULL,       -- Tier 2: e.g., "CASE_02", "SHELF_B"
    box_code TEXT NOT NULL,        -- Tier 3: e.g., "BOX_04" (Monster box, Slab case)
    row_code TEXT NOT NULL,        -- Tier 4: e.g., "ROW_2"
    slot_number INTEGER NOT NULL,  -- Tier 5: Sequential slot/divider position (1..N)
    capacity INTEGER NOT NULL DEFAULT 1,
    occupied_count INTEGER NOT NULL DEFAULT 0,
    barcode_label TEXT UNIQUE NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(vault_code, case_code, box_code, row_code, slot_number)
);

-- 2. Master Reference Checklists (Ground-Truth for Fuzzy Matching & RAG)
CREATE TABLE IF NOT EXISTS master_checklists (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    category TEXT NOT NULL CHECK(category IN ('BASEBALL', 'BASKETBALL', 'FOOTBALL', 'HOCKEY', 'GOLF', 'POKEMON', 'YUGIOH', 'MISC_TCG')),
    release_year INTEGER NOT NULL,
    manufacturer TEXT NOT NULL,        -- e.g., "Panini", "Topps", "Upper Deck", "Pokemon Company"
    set_name TEXT NOT NULL,            -- e.g., "Prizm", "Topps Chrome", "Base Set"
    card_number TEXT NOT NULL,         -- e.g., "25", "US175", "102/102", "SV49"
    player_or_character TEXT NOT NULL, -- e.g., "Victor Wembanyama", "Charizard"
    team_or_type TEXT,                 -- e.g., "San Antonio Spurs", "Fire / Stage 2"
    is_rookie_base BOOLEAN NOT NULL DEFAULT 0,
    known_parallels_json TEXT NOT NULL DEFAULT '["Base"]', -- JSON array of valid parallel names
    UNIQUE(category, release_year, manufacturer, set_name, card_number, player_or_character)
);

-- Full-Text Search Virtual Table for rapid OCR candidate retrieval
CREATE VIRTUAL TABLE IF NOT EXISTS master_checklists_fts USING fts5(
    category,
    release_year,
    manufacturer,
    set_name,
    card_number,
    player_or_character,
    team_or_type,
    content='master_checklists',
    content_rowid='id'
);

-- Triggers to keep FTS5 synchronized with master_checklists
CREATE TRIGGER IF NOT EXISTS master_checklists_ai AFTER INSERT ON master_checklists BEGIN
    INSERT INTO master_checklists_fts(rowid, category, release_year, manufacturer, set_name, card_number, player_or_character, team_or_type)
    VALUES (new.id, new.category, new.release_year, new.manufacturer, new.set_name, new.card_number, new.player_or_character, new.team_or_type);
END;

-- 3. Durable Ingestion Job Queue
CREATE TABLE IF NOT EXISTS ingestion_jobs (
    id TEXT PRIMARY KEY, -- UUIDv4
    batch_id TEXT NOT NULL,
    front_image_path TEXT NOT NULL,
    back_image_path TEXT,
    cropped_front_path TEXT,
    cropped_back_path TEXT,
    status TEXT NOT NULL CHECK(status IN ('PENDING', 'PREPROCESSING', 'EXTRACTING', 'MATCHING', 'NEEDS_REVIEW', 'COMPLETED', 'FAILED')),
    extractor_used TEXT, -- 'TESSERACT_LOCAL', 'OLLAMA_VLM', 'CLAUDE_API'
    raw_ocr_front TEXT,
    raw_ocr_back TEXT,
    extracted_payload_json TEXT, -- Matches CardExtractionPayload contract
    confidence_score REAL NOT NULL DEFAULT 0.0,
    error_message TEXT,
    retry_count INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 4. Shop Card Inventory (Physical Assets)
CREATE TABLE IF NOT EXISTS inventory_cards (
    id TEXT PRIMARY KEY, -- UUIDv4
    sku TEXT UNIQUE NOT NULL, -- e.g., "HY-FB-2023-PRZ-301-001"
    ingestion_job_id TEXT REFERENCES ingestion_jobs(id),
    checklist_id INTEGER REFERENCES master_checklists(id),
    storage_location_id TEXT REFERENCES storage_locations(id),
    
    -- Core Identity
    category TEXT NOT NULL CHECK(category IN ('BASEBALL', 'BASKETBALL', 'FOOTBALL', 'HOCKEY', 'GOLF', 'POKEMON', 'YUGIOH', 'MISC_TCG')),
    release_year INTEGER NOT NULL,
    manufacturer TEXT NOT NULL,
    set_name TEXT NOT NULL,
    card_number TEXT NOT NULL,
    player_or_character TEXT NOT NULL,
    team_or_type TEXT,
    parallel_variant TEXT NOT NULL DEFAULT 'Base', -- e.g., "Silver Prizm", "Holo Rare", "Refractor"
    serial_number TEXT,                            -- e.g., "045/199", NULL if unnumbered
    serial_max INTEGER,                            -- e.g., 199, NULL if unnumbered
    is_rookie BOOLEAN NOT NULL DEFAULT 0,
    is_autograph BOOLEAN NOT NULL DEFAULT 0,
    is_memorabilia BOOLEAN NOT NULL DEFAULT 0,
    
    -- Condition & Grading
    is_graded BOOLEAN NOT NULL DEFAULT 0,
    grading_company TEXT, -- 'RAW', 'PSA', 'BGS', 'SGC', 'CGC'
    grade_value REAL,     -- e.g., 10.0, 9.5, 8.0
    cert_number TEXT,
    estimated_raw_condition TEXT CHECK(estimated_raw_condition IN ('NM-MT', 'NM', 'EX-MT', 'VG-EX', 'POOR')),
    centering_ratio_lr TEXT, -- e.g., "52/48"
    centering_ratio_tb TEXT, -- e.g., "55/45"
    corner_wear_score REAL,  -- 0.0 (sharp) to 1.0 (heavy whitening)
    
    -- Valuation & Hoot-Score Engine
    market_price_cents INTEGER NOT NULL DEFAULT 0,
    acquisition_cost_cents INTEGER NOT NULL DEFAULT 0,
    asking_price_cents INTEGER NOT NULL DEFAULT 0,
    player_impact_tier INTEGER NOT NULL DEFAULT 3 CHECK(player_impact_tier BETWEEN 1 AND 5), -- 1=HOF/Superstar, 5=Common
    scarcity_multiplier REAL NOT NULL DEFAULT 1.0,
    hoot_score REAL NOT NULL DEFAULT 0.0, -- Composite score (0.0 to 100.0)
    
    -- Listing & Status
    inventory_status TEXT NOT NULL DEFAULT 'IN_REVIEW' CHECK(inventory_status IN ('IN_REVIEW', 'IN_STOCK', 'LISTED', 'SOLD', 'VAULT_HOLD')),
    marketplace_meta_json TEXT NOT NULL DEFAULT '{}', -- eBay/TCGplayer title, category_id, condition_id
    front_image_url TEXT NOT NULL,
    back_image_url TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 5. Historical Market Price Snapshots
CREATE TABLE IF NOT EXISTS price_snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    inventory_card_id TEXT NOT NULL REFERENCES inventory_cards(id) ON DELETE CASCADE,
    source TEXT NOT NULL CHECK(source IN ('EBAY_SOLD', 'TCGPLAYER_MARKET', 'PRICECHARTING', 'MANUAL')),
    price_cents INTEGER NOT NULL,
    listing_url TEXT,
    captured_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_jobs_status ON ingestion_jobs(status);
CREATE INDEX IF NOT EXISTS idx_inventory_status ON inventory_cards(inventory_status);
CREATE INDEX IF NOT EXISTS idx_inventory_category_year ON inventory_cards(category, release_year);
CREATE INDEX IF NOT EXISTS idx_inventory_hoot_score ON inventory_cards(hoot_score DESC);
