package main

import "testing"

func TestSelectLocationsSplitAll(t *testing.T) {
	locations, err := parseLocationNames([]string{"Southroads", "Utica", "Downtown"})
	if err != nil {
		t.Fatal(err)
	}

	selected, err := selectLocations(locations, "split")
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 3 {
		t.Fatalf("expected 3 selected locations, got %d", len(selected))
	}
}

func TestSelectLocationsSubset(t *testing.T) {
	locations, err := parseLocationNames([]string{"Southroads", "Utica", "Downtown"})
	if err != nil {
		t.Fatal(err)
	}

	selected, err := selectLocations(locations, "split-Southroads-Utica")
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 2 {
		t.Fatalf("expected 2 selected locations, got %d", len(selected))
	}
	if selected[0].Slug != "southroads" || selected[1].Slug != "utica" {
		t.Fatalf("unexpected locations: %#v", selected)
	}
}

func TestParseLocationNamesRejectsDuplicates(t *testing.T) {
	_, err := parseLocationNames([]string{"Southroads", "south roads"})
	if err == nil {
		t.Fatal("expected duplicate location error")
	}
}
