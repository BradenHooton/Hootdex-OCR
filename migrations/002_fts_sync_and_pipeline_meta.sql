-- 002: Complete the FTS5 external-content synchronisation and add pipeline metadata.
--
-- 001 only ships the AFTER INSERT trigger. An external-content FTS5 table must also be
-- told about deletes and updates (via the special 'delete' command carrying the OLD
-- values), otherwise the index returns stale or phantom rows.

CREATE TRIGGER IF NOT EXISTS master_checklists_ad AFTER DELETE ON master_checklists BEGIN
    INSERT INTO master_checklists_fts(master_checklists_fts, rowid, category, release_year, manufacturer, set_name, card_number, player_or_character, team_or_type)
    VALUES ('delete', old.id, old.category, old.release_year, old.manufacturer, old.set_name, old.card_number, old.player_or_character, old.team_or_type);
END;

CREATE TRIGGER IF NOT EXISTS master_checklists_au AFTER UPDATE ON master_checklists BEGIN
    INSERT INTO master_checklists_fts(master_checklists_fts, rowid, category, release_year, manufacturer, set_name, card_number, player_or_character, team_or_type)
    VALUES ('delete', old.id, old.category, old.release_year, old.manufacturer, old.set_name, old.card_number, old.player_or_character, old.team_or_type);
    INSERT INTO master_checklists_fts(rowid, category, release_year, manufacturer, set_name, card_number, player_or_character, team_or_type)
    VALUES (new.id, new.category, new.release_year, new.manufacturer, new.set_name, new.card_number, new.player_or_character, new.team_or_type);
END;

-- Reconcile the index with any rows written before these triggers existed.
INSERT INTO master_checklists_fts(master_checklists_fts) VALUES ('rebuild');

-- Preprocessing output (card type, zone image paths, centering, corner wear) for the review UI.
ALTER TABLE ingestion_jobs ADD COLUMN preprocess_meta_json TEXT;

-- FIFO claim order for the worker pool.
CREATE INDEX IF NOT EXISTS idx_jobs_status_created ON ingestion_jobs(status, created_at);
CREATE INDEX IF NOT EXISTS idx_inventory_storage ON inventory_cards(storage_location_id);
CREATE INDEX IF NOT EXISTS idx_inventory_job ON inventory_cards(ingestion_job_id);
