// Package seed loads realistic reference data (master checklist + storage hierarchy)
// through the repository ports. It is used by the seed CLI, the OCR test runner and tests.
package seed

import (
	"context"
	"fmt"

	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/ports"
)

var (
	footballParallels = []string{"Base", "Silver Prizm", "Red White and Blue Prizm", "Green Prizm", "Orange Prizm", "Blue Prizm", "Red Prizm", "Gold Prizm", "Black Finite Prizm"}
	chromeParallels   = []string{"Base", "Refractor", "Prism Refractor", "Sepia Refractor", "Pink Refractor", "Blue Refractor", "Gold Refractor", "Orange Refractor", "Red Refractor", "Superfractor"}
	ygParallels       = []string{"Base", "Exclusives", "High Gloss", "Clear Cut", "French", "Canvas"}
	wotcParallels     = []string{"Base", "Holo", "1st Edition Holo", "Shadowless Holo"}
	sv151Parallels    = []string{"Base", "Reverse Holo", "Holo", "Illustration Rare", "Special Illustration Rare", "Hyper Rare"}
)

func fb(num, player, team string, rc bool) domain.ChecklistEntry {
	return domain.ChecklistEntry{Category: domain.CategoryFootball, ReleaseYear: 2023, Manufacturer: "Panini", SetName: "Prizm",
		CardNumber: num, PlayerOrCharacter: player, TeamOrType: team, IsRookieBase: rc, KnownParallels: footballParallels}
}

func bb(num, player, team string, rc bool) domain.ChecklistEntry {
	return domain.ChecklistEntry{Category: domain.CategoryBaseball, ReleaseYear: 2024, Manufacturer: "Topps", SetName: "Topps Chrome",
		CardNumber: num, PlayerOrCharacter: player, TeamOrType: team, IsRookieBase: rc, KnownParallels: chromeParallels}
}

func hk(num, player, team string) domain.ChecklistEntry {
	return domain.ChecklistEntry{Category: domain.CategoryHockey, ReleaseYear: 2023, Manufacturer: "Upper Deck", SetName: "Young Guns",
		CardNumber: num, PlayerOrCharacter: player, TeamOrType: team, IsRookieBase: true, KnownParallels: ygParallels}
}

func pkBase(num, name, typ string) domain.ChecklistEntry {
	return domain.ChecklistEntry{Category: domain.CategoryPokemon, ReleaseYear: 1999, Manufacturer: "Wizards of the Coast", SetName: "Base Set",
		CardNumber: num, PlayerOrCharacter: name, TeamOrType: typ, KnownParallels: wotcParallels}
}

func pk151(num, name, typ string) domain.ChecklistEntry {
	return domain.ChecklistEntry{Category: domain.CategoryPokemon, ReleaseYear: 2023, Manufacturer: "Pokemon Company", SetName: "Scarlet & Violet 151",
		CardNumber: num, PlayerOrCharacter: name, TeamOrType: typ, KnownParallels: sv151Parallels}
}

// Checklist returns the 50 reference checklist rows.
func Checklist() []domain.ChecklistEntry {
	return []domain.ChecklistEntry{
		// Football — 2023 Panini Prizm (13)
		fb("339", "C.J. Stroud", "Houston Texans", true),
		fb("301", "Bryce Young", "Carolina Panthers", true),
		fb("331", "Anthony Richardson", "Indianapolis Colts", true),
		fb("340", "Bijan Robinson", "Atlanta Falcons", true),
		fb("349", "Jahmyr Gibbs", "Detroit Lions", true),
		fb("343", "Puka Nacua", "Los Angeles Rams", true),
		fb("334", "Jaxon Smith-Njigba", "Seattle Seahawks", true),
		fb("1", "Patrick Mahomes II", "Kansas City Chiefs", false),
		fb("6", "Josh Allen", "Buffalo Bills", false),
		fb("100", "Justin Jefferson", "Minnesota Vikings", false),
		fb("150", "Joe Burrow", "Cincinnati Bengals", false),
		fb("200", "Jalen Hurts", "Philadelphia Eagles", false),
		fb("250", "Travis Kelce", "Kansas City Chiefs", false),
		// Baseball — 2024 Topps Chrome (12)
		bb("1", "Shohei Ohtani", "Los Angeles Dodgers", false),
		bb("10", "Aaron Judge", "New York Yankees", false),
		bb("27", "Mike Trout", "Los Angeles Angels", false),
		bb("50", "Ronald Acuna Jr.", "Atlanta Braves", false),
		bb("75", "Juan Soto", "New York Yankees", false),
		bb("100", "Julio Rodriguez", "Seattle Mariners", false),
		bb("150", "Bobby Witt Jr.", "Kansas City Royals", false),
		bb("200", "Gunnar Henderson", "Baltimore Orioles", false),
		bb("220", "Jackson Holliday", "Baltimore Orioles", true),
		bb("250", "Wyatt Langford", "Texas Rangers", true),
		bb("260", "Evan Carter", "Texas Rangers", true),
		bb("270", "Jackson Chourio", "Milwaukee Brewers", true),
		// Hockey — 2023-24 Upper Deck Young Guns (10)
		hk("201", "Connor Bedard", "Chicago Blackhawks"),
		hk("203", "Logan Cooley", "Arizona Coyotes"),
		hk("204", "Luke Hughes", "New Jersey Devils"),
		hk("205", "Leo Carlsson", "Anaheim Ducks"),
		hk("211", "Pavel Mintyukov", "Anaheim Ducks"),
		hk("215", "Kevin Korchinski", "Chicago Blackhawks"),
		hk("451", "Adam Fantilli", "Columbus Blue Jackets"),
		hk("452", "Brock Faber", "Minnesota Wild"),
		hk("456", "Marco Rossi", "Minnesota Wild"),
		hk("461", "Dmitri Voronkov", "Columbus Blue Jackets"),
		// Pokémon — 1999 Base Set (8)
		pkBase("4/102", "Charizard", "Fire / Stage 2"),
		pkBase("2/102", "Blastoise", "Water / Stage 2"),
		pkBase("15/102", "Venusaur", "Grass / Stage 2"),
		pkBase("10/102", "Mewtwo", "Psychic / Basic"),
		pkBase("58/102", "Pikachu", "Lightning / Basic"),
		pkBase("1/102", "Alakazam", "Psychic / Stage 2"),
		pkBase("16/102", "Zapdos", "Lightning / Basic"),
		pkBase("7/102", "Hitmonchan", "Fighting / Basic"),
		// Pokémon — Scarlet & Violet 151 (7)
		pk151("001/165", "Bulbasaur", "Grass / Basic"),
		pk151("006/165", "Charizard ex", "Fire / Stage 2"),
		pk151("025/165", "Pikachu", "Lightning / Basic"),
		pk151("151/165", "Mew ex", "Psychic / Basic"),
		pk151("199/165", "Charizard ex", "Fire / Stage 2"),
		pk151("200/165", "Blastoise ex", "Water / Stage 2"),
		pk151("205/165", "Mew ex", "Psychic / Basic"),
	}
}

// StorageLocations returns 20 slots: VAULT_A / CASE_01 / BOX_01..02 / ROW_1..2 / slots 1..5.
func StorageLocations() ([]domain.StorageLocation, error) {
	var out []domain.StorageLocation
	for box := 1; box <= 2; box++ {
		for row := 1; row <= 2; row++ {
			for slot := 1; slot <= 5; slot++ {
				l, err := domain.NewStorageLocation("VAULT_A", "CASE_01", fmt.Sprintf("BOX_%02d", box), fmt.Sprintf("ROW_%d", row), slot, 25)
				if err != nil {
					return nil, err
				}
				out = append(out, l)
			}
		}
	}
	return out, nil
}

// Result summarises a seed run.
type Result struct {
	ChecklistRows int
	Locations     int
}

// Apply idempotently upserts the reference data.
func Apply(ctx context.Context, checklist ports.ChecklistRepository, storage ports.StorageRepository) (Result, error) {
	var res Result
	for _, e := range Checklist() {
		e := e
		if err := checklist.Upsert(ctx, &e); err != nil {
			return res, fmt.Errorf("seed.Apply: checklist: %w", err)
		}
		res.ChecklistRows++
	}
	locs, err := StorageLocations()
	if err != nil {
		return res, fmt.Errorf("seed.Apply: storage: %w", err)
	}
	for _, l := range locs {
		if err := storage.Upsert(ctx, l); err != nil {
			return res, fmt.Errorf("seed.Apply: storage: %w", err)
		}
		res.Locations++
	}
	return res, nil
}
