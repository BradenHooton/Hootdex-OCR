package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// StorageLocation is a 5-tier physical slot: Vault -> Case -> Box -> Row -> Slot.
type StorageLocation struct {
	ID            string    `json:"id"`
	VaultCode     string    `json:"vault_code"`
	CaseCode      string    `json:"case_code"`
	BoxCode       string    `json:"box_code"`
	RowCode       string    `json:"row_code"`
	SlotNumber    int       `json:"slot_number"`
	Capacity      int       `json:"capacity"`
	OccupiedCount int       `json:"occupied_count"`
	BarcodeLabel  string    `json:"barcode_label"`
	CreatedAt     time.Time `json:"created_at"`
}

// NewStorageLocation builds a location with a deterministic slug ID and barcode.
func NewStorageLocation(vault, caseCode, box, row string, slot, capacity int) (StorageLocation, error) {
	l := StorageLocation{
		VaultCode:  strings.ToUpper(strings.TrimSpace(vault)),
		CaseCode:   strings.ToUpper(strings.TrimSpace(caseCode)),
		BoxCode:    strings.ToUpper(strings.TrimSpace(box)),
		RowCode:    strings.ToUpper(strings.TrimSpace(row)),
		SlotNumber: slot,
		Capacity:   capacity,
	}
	if err := l.Validate(); err != nil {
		return StorageLocation{}, err
	}
	l.ID = l.Slug()
	l.BarcodeLabel = "HY-LOC-" + l.ID
	return l, nil
}

// Validate checks structural invariants.
func (l StorageLocation) Validate() error {
	switch {
	case l.VaultCode == "" || l.CaseCode == "" || l.BoxCode == "" || l.RowCode == "":
		return fmt.Errorf("%w: storage tiers vault/case/box/row are required", ErrInvalid)
	case l.SlotNumber < 1:
		return fmt.Errorf("%w: slot_number must be >= 1", ErrInvalid)
	case l.Capacity < 1:
		return fmt.Errorf("%w: capacity must be >= 1", ErrInvalid)
	case l.OccupiedCount < 0 || l.OccupiedCount > l.Capacity:
		return fmt.Errorf("%w: occupied_count %d outside 0..%d", ErrInvalid, l.OccupiedCount, l.Capacity)
	}
	return nil
}

// Slug renders the deterministic ID, e.g. "VA-C02-B04-R2-S015" style: "VAULT_A-CASE_02-BOX_04-ROW_2-S015".
func (l StorageLocation) Slug() string {
	return fmt.Sprintf("%s-%s-%s-%s-S%03d", l.VaultCode, l.CaseCode, l.BoxCode, l.RowCode, l.SlotNumber)
}

// Path renders the human-readable hierarchy.
func (l StorageLocation) Path() string {
	return fmt.Sprintf("%s › %s › %s › %s › Slot %d", l.VaultCode, l.CaseCode, l.BoxCode, l.RowCode, l.SlotNumber)
}

// Available reports remaining capacity.
func (l StorageLocation) Available() int { return l.Capacity - l.OccupiedCount }

// HasCapacity reports whether at least one more card fits.
func (l StorageLocation) HasCapacity() bool { return l.Available() > 0 }

// Occupy reserves one unit of capacity.
func (l *StorageLocation) Occupy() error {
	if !l.HasCapacity() {
		return fmt.Errorf("storage %s: %w", l.ID, ErrNoStorageCapacity)
	}
	l.OccupiedCount++
	return nil
}

// Release frees one unit of capacity.
func (l *StorageLocation) Release() error {
	if l.OccupiedCount == 0 {
		return fmt.Errorf("%w: storage %s already empty", ErrInvalid, l.ID)
	}
	l.OccupiedCount--
	return nil
}

// LocationLess is the canonical fill order: vault, case, box, row, then slot number.
// Repositories must mirror this ordering (ORDER BY vault_code, case_code, box_code, row_code, slot_number).
func LocationLess(a, b StorageLocation) bool {
	if a.VaultCode != b.VaultCode {
		return a.VaultCode < b.VaultCode
	}
	if a.CaseCode != b.CaseCode {
		return a.CaseCode < b.CaseCode
	}
	if a.BoxCode != b.BoxCode {
		return a.BoxCode < b.BoxCode
	}
	if a.RowCode != b.RowCode {
		return a.RowCode < b.RowCode
	}
	return a.SlotNumber < b.SlotNumber
}

// NextAvailableLocation picks the first location (in canonical fill order) with free capacity.
func NextAvailableLocation(locs []StorageLocation) (StorageLocation, error) {
	sorted := make([]StorageLocation, len(locs))
	copy(sorted, locs)
	sort.SliceStable(sorted, func(i, j int) bool { return LocationLess(sorted[i], sorted[j]) })
	for _, l := range sorted {
		if l.HasCapacity() {
			return l, nil
		}
	}
	return StorageLocation{}, ErrNoStorageCapacity
}
