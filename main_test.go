package main

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseLocationNamesRejectsDuplicates(t *testing.T) {
	_, err := parseLocationNames([]string{"Southroads", "south roads"})
	if err == nil {
		t.Fatal("expected duplicate location error")
	}
}

func TestSplitLineItemPreservesDefaultPennyBehavior(t *testing.T) {
	locations, err := parseLocationNames([]string{"Southroads", "Utica"})
	if err != nil {
		t.Fatal(err)
	}
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

func TestInferLocationsFromConcreteReceiptNames(t *testing.T) {
	root := t.TempDir()
	writeTestPDF(t, filepath.Join(root, "010125-Vendor-3.01-Paper-Office-north.pdf"))
	writeTestPDF(t, filepath.Join(root, "010225-Vendor-6-Supplies-Office-split.pdf"))
	writeTestPDF(t, filepath.Join(root, "010325-Vendor-4-Food-Meals-south.pdf"))

	locations, err := inferLocations(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(locations) != 2 {
		t.Fatalf("expected 2 inferred locations, got %d", len(locations))
	}
	if locations[0].Slug != "north" || locations[1].Slug != "south" {
		t.Fatalf("unexpected inferred locations: %#v", locations)
	}
}

func TestInferLocationsRejectsSplitOnlyFolders(t *testing.T) {
	root := t.TempDir()
	writeTestPDF(t, filepath.Join(root, "010225-Vendor-6-Supplies-Office-split.pdf"))

	_, err := inferLocations(root)
	if err == nil {
		t.Fatal("expected split-only folder to fail location inference")
	}
}

func TestConsumeGenerationAllowsTenUsesPerWindow(t *testing.T) {
	db := newTestDB(t)
	application := &app{db: db}

	for i := 0; i < maxUsesPerIP; i++ {
		ok, err := application.consumeGeneration(testRequestFromIP("203.0.113.10"))
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("generation %d was unexpectedly limited", i+1)
		}
	}

	ok, err := application.consumeGeneration(testRequestFromIP("203.0.113.10"))
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected eleventh generation to be rate limited")
	}
}

func TestConsumeGenerationPrunesExpiredUsage(t *testing.T) {
	db := newTestDB(t)
	application := &app{db: db}

	expired := time.Now().Add(-usageWindow).Add(-time.Minute).Unix()
	if _, err := db.Exec(`INSERT INTO invoice_usage (ip, used_at) VALUES (?, ?)`, "203.0.113.20", expired); err != nil {
		t.Fatal(err)
	}

	ok, err := application.consumeGeneration(testRequestFromIP("203.0.113.20"))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected expired usage to be pruned before limiting")
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM invoice_usage WHERE used_at < ?`, time.Now().Add(-usageWindow).Unix()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected expired usage rows to be pruned, got %d", count)
	}
}

func TestFormInvoiceAllowsTwentyFiveUsesPerWindow(t *testing.T) {
	db := newTestDB(t)
	application := &app{db: db}
	req := testRequestFromIP("203.0.113.30")
	for i := 0; i < maxFormInvoicesPerIP; i++ {
		ok, err := application.consumeUsage(req, "form_invoice_usage", maxFormInvoicesPerIP)
		if err != nil || !ok {
			t.Fatalf("form invoice %d was unexpectedly limited: ok=%v err=%v", i+1, ok, err)
		}
	}
	ok, err := application.consumeUsage(req, "form_invoice_usage", maxFormInvoicesPerIP)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected twenty-sixth form invoice to be rate limited")
	}
}

func TestParseFormInvoiceSupportsMultipleSplitLocations(t *testing.T) {
	body := strings.NewReader("invoice_name=September+expenses&invoice_date=2026-09-01&locations=North&locations=South&locations=West&line_3_date=2026-08-31&line_3_vendor=Acme&line_3_description=Paper&line_3_category=Office&line_3_amount=10.01&line_locations_3=0&line_locations_3=1&line_locations_3=2")
	req := httptest.NewRequest(http.MethodPost, "/invoices", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	invoice, err := parseFormInvoice(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(invoice.Items) != 1 || len(invoice.Items[0].Locations) != 3 {
		t.Fatalf("unexpected parsed invoice: %#v", invoice)
	}
	if invoice.Items[0].CostCents != 1001 {
		t.Fatalf("expected 1001 cents, got %d", invoice.Items[0].CostCents)
	}

	path := filepath.Join(t.TempDir(), "invoice.pdf")
	if err := generateFormInvoicePDF(path, invoice); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		t.Fatalf("expected generated PDF, info=%v err=%v", info, err)
	}
}

func TestPruneJobsRemovesExpiredJobDirectories(t *testing.T) {
	root := t.TempDir()
	oldJob := filepath.Join(root, time.Now().Add(-48*time.Hour).UTC().Format("20060102T150405Z")+"-old")
	newJob := filepath.Join(root, time.Now().UTC().Format("20060102T150405Z")+"-new")
	if err := os.MkdirAll(oldJob, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newJob, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := pruneJobs(root, time.Now().Add(-jobTTL)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldJob); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected old job to be removed, stat err: %v", err)
	}
	if _, err := os.Stat(newJob); err != nil {
		t.Fatalf("expected new job to remain: %v", err)
	}
}

func TestMergePDFsIdentifiesInvalidInput(t *testing.T) {
	root := t.TempDir()
	badPDF := filepath.Join(root, "010125-Vendor-3.01-Paper-Office-north.pdf")
	if err := os.WriteFile(badPDF, []byte("this is not actually a PDF"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := mergePDFs(filepath.Join(root, "merged.pdf"), []string{badPDF})
	if err == nil {
		t.Fatal("expected invalid PDF to fail")
	}
	if !strings.Contains(err.Error(), filepath.Base(badPDF)) {
		t.Fatalf("expected error to identify %q, got %q", filepath.Base(badPDF), err)
	}
}

func writeTestPDF(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("%PDF-1.4\n%%EOF\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := initDB(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func testRequestFromIP(ip string) *http.Request {
	req := httptest.NewRequest("POST", "/jobs", nil)
	req.RemoteAddr = ip + ":12345"
	return req
}
