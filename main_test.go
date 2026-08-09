package main

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestSplitLineItemPreservesDefaultPennyBehavior(t *testing.T) {
	locations := defaultLocations()
	item := invoiceLineItem{
		Date:        "010125",
		Vendor:      "Vendor",
		CostCents:   101,
		Description: "Thing",
		Category:    "Ops",
		Location:    "split",
	}

	splits, err := splitLineItem(item, locations)
	if err != nil {
		t.Fatal(err)
	}
	if len(splits) != 2 {
		t.Fatalf("expected 2 splits, got %d", len(splits))
	}
	if splits[0].Location != "southroads" || splits[0].CostCents != 50 {
		t.Fatalf("unexpected southroads split: %#v", splits[0])
	}
	if splits[1].Location != "utica" || splits[1].CostCents != 51 {
		t.Fatalf("unexpected utica split: %#v", splits[1])
	}
}

func TestSortReceiptsSupportsConfiguredLocations(t *testing.T) {
	root := t.TempDir()
	uploadDir := filepath.Join(root, "upload")
	outDir := filepath.Join(root, "sorted")
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		t.Fatal(err)
	}

	writeTestPDF(t, filepath.Join(uploadDir, "010125-Vendor-3.01-Paper-Office-north.pdf"))
	writeTestPDF(t, filepath.Join(uploadDir, "010225-Vendor-6-Supplies-Office-split.pdf"))

	locations, err := parseLocationNames([]string{"North", "South", "West"})
	if err != nil {
		t.Fatal(err)
	}
	if err := sortReceipts(uploadDir, outDir, locations); err != nil {
		t.Fatal(err)
	}

	expected := []string{
		filepath.Join(outDir, "north", "010125-Vendor-3.01-Paper-Office-north.pdf"),
		filepath.Join(outDir, "north", "010225-Vendor-2-Supplies-Office-north.pdf"),
		filepath.Join(outDir, "south", "010225-Vendor-2-Supplies-Office-south.pdf"),
		filepath.Join(outDir, "west", "010225-Vendor-2-Supplies-Office-west.pdf"),
	}
	for _, path := range expected {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected sorted receipt %s: %v", path, err)
		}
	}
}

func writeTestPDF(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("%PDF-1.4\n%%EOF\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
