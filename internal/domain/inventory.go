package domain

// InventoryFilter narrows inventory listings. Zero values mean "no filter".
type InventoryFilter struct {
	Category     Category
	ReleaseYear  int
	MinHootScore float64
	MaxHootScore float64
	BoxCode      string
	Status       InventoryStatus
	Search       string // matches SKU, player, set or manufacturer
	Limit        int
	Offset       int
}

// InventoryRecord is a card joined with its physical storage location (nil when unassigned).
type InventoryRecord struct {
	Card     *Card
	Location *StorageLocation
}
