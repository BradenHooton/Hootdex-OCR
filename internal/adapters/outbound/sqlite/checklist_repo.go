package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/ports"
)

// MaxCandidates is how many ranked candidates MatchCandidates returns (top 15 feed the LLM).
const MaxCandidates = 15

const (
	ftsRetrieveLimit   = 200  // bm25 pre-filter size
	fallbackScanLimit  = 2000 // year/category scan when OCR tokens are too garbled for FTS
	fallbackFTSMinimum = 20   // below this many FTS hits we widen the net
)

// ChecklistRepository implements ports.ChecklistRepository using FTS5 retrieval and
// Levenshtein re-ranking.
type ChecklistRepository struct{ db *DB }

var _ ports.ChecklistRepository = (*ChecklistRepository)(nil)

// NewChecklistRepository constructs the repository.
func NewChecklistRepository(db *DB) *ChecklistRepository { return &ChecklistRepository{db: db} }

const checklistCols = `id, category, release_year, manufacturer, set_name, card_number, player_or_character, team_or_type, is_rookie_base, known_parallels_json`

func scanChecklist(r rowScanner) (domain.ChecklistEntry, error) {
	var e domain.ChecklistEntry
	var team sql.NullString
	var parallels string
	if err := r.Scan(&e.ID, &e.Category, &e.ReleaseYear, &e.Manufacturer, &e.SetName, &e.CardNumber, &e.PlayerOrCharacter, &team, &e.IsRookieBase, &parallels); err != nil {
		return e, err
	}
	e.TeamOrType = team.String
	if err := json.Unmarshal([]byte(parallels), &e.KnownParallels); err != nil {
		return e, fmt.Errorf("known_parallels_json for %d: %w", e.ID, err)
	}
	return e, nil
}

// Upsert inserts or updates by natural key; the FTS triggers keep the index in sync.
func (r *ChecklistRepository) Upsert(ctx context.Context, e *domain.ChecklistEntry) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("sqlite.ChecklistRepository.Upsert [%s]: %w", e.DisplayName(), err)
	}
	if len(e.KnownParallels) == 0 {
		e.KnownParallels = []string{domain.BaseVariant}
	}
	parallels, err := json.Marshal(e.KnownParallels)
	if err != nil {
		return fmt.Errorf("sqlite.ChecklistRepository.Upsert [%s]: %w", e.DisplayName(), err)
	}
	err = r.db.QueryRowContext(ctx, `INSERT INTO master_checklists
		(category, release_year, manufacturer, set_name, card_number, player_or_character, team_or_type, is_rookie_base, known_parallels_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(category, release_year, manufacturer, set_name, card_number, player_or_character)
		DO UPDATE SET team_or_type = excluded.team_or_type, is_rookie_base = excluded.is_rookie_base,
			known_parallels_json = excluded.known_parallels_json
		RETURNING id`,
		e.Category, e.ReleaseYear, e.Manufacturer, e.SetName, e.CardNumber, e.PlayerOrCharacter, nullStr(e.TeamOrType), e.IsRookieBase, string(parallels),
	).Scan(&e.ID)
	if err != nil {
		return fmt.Errorf("sqlite.ChecklistRepository.Upsert [%s]: %w", e.DisplayName(), err)
	}
	return nil
}

// Get loads one entry.
func (r *ChecklistRepository) Get(ctx context.Context, id int64) (*domain.ChecklistEntry, error) {
	e, err := scanChecklist(r.db.QueryRowContext(ctx, `SELECT `+checklistCols+` FROM master_checklists WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("sqlite.ChecklistRepository.Get [%d]: %w", id, domain.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite.ChecklistRepository.Get [%d]: %w", id, err)
	}
	return &e, nil
}

// Count returns the number of checklist rows.
func (r *ChecklistRepository) Count(ctx context.Context) (int, error) {
	var n int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM master_checklists`).Scan(&n); err != nil {
		return 0, fmt.Errorf("sqlite.ChecklistRepository.Count: %w", err)
	}
	return n, nil
}

// MatchCandidates retrieves candidates with FTS5 (bm25) — widened by a year/category scan when
// OCR noise defeats the full-text tokens — then re-ranks them with Levenshtein field scoring.
func (r *ChecklistRepository) MatchCandidates(ctx context.Context, rawText string, yearHint int, categoryHint domain.Category) ([]domain.ChecklistCandidate, error) {
	q := parseMatchQuery(rawText, yearHint, categoryHint)
	if len(q.tokens) == 0 && yearHint == 0 && categoryHint == "" {
		return nil, nil
	}
	pool := map[int64]domain.ChecklistEntry{}
	if ftsQ := q.ftsExpression(); ftsQ != "" {
		rows, err := r.db.QueryContext(ctx, `SELECT `+prefixed("m.", checklistCols)+`
			FROM master_checklists_fts f JOIN master_checklists m ON m.id = f.rowid
			WHERE master_checklists_fts MATCH ? ORDER BY bm25(master_checklists_fts) LIMIT ?`, ftsQ, ftsRetrieveLimit)
		if err != nil {
			return nil, fmt.Errorf("sqlite.ChecklistRepository.MatchCandidates: fts: %w", err)
		}
		if err := collectEntries(rows, pool); err != nil {
			return nil, fmt.Errorf("sqlite.ChecklistRepository.MatchCandidates: fts: %w", err)
		}
	}
	if len(pool) < fallbackFTSMinimum {
		where, args := []string{}, []any{}
		if years := q.allYears(); len(years) > 0 {
			where = append(where, "release_year IN ("+placeholders(len(years))+")")
			for _, y := range years {
				args = append(args, y)
			}
		}
		if categoryHint != "" {
			where = append(where, "category = ?")
			args = append(args, categoryHint)
		}
		sqlText := `SELECT ` + checklistCols + ` FROM master_checklists`
		if len(where) > 0 {
			sqlText += " WHERE " + strings.Join(where, " OR ")
		}
		rows, err := r.db.QueryContext(ctx, sqlText+` LIMIT ?`, append(args, fallbackScanLimit)...)
		if err != nil {
			return nil, fmt.Errorf("sqlite.ChecklistRepository.MatchCandidates: fallback: %w", err)
		}
		if err := collectEntries(rows, pool); err != nil {
			return nil, fmt.Errorf("sqlite.ChecklistRepository.MatchCandidates: fallback: %w", err)
		}
	}
	out := make([]domain.ChecklistCandidate, 0, len(pool))
	for _, e := range pool {
		out = append(out, domain.ChecklistCandidate{Entry: e, Score: q.score(e)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Entry.ID < out[j].Entry.ID
	})
	if len(out) > MaxCandidates {
		out = out[:MaxCandidates]
	}
	return out, nil
}

func collectEntries(rows *sql.Rows, pool map[int64]domain.ChecklistEntry) error {
	defer rows.Close()
	for rows.Next() {
		e, err := scanChecklist(rows)
		if err != nil {
			return err
		}
		pool[e.ID] = e
	}
	return rows.Err()
}

func prefixed(p, cols string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = p + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}

func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }
