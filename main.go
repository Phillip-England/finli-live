package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

const (
	maxUpload = 512 << 20
	dataDir   = "data/jobs"
)

var page = template.Must(template.New("index").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Finli Live</title>
  <style>
    :root {
      color-scheme: light;
      --ink: #17201a;
      --muted: #68746d;
      --line: #d8ded9;
      --panel: #f7f8f5;
      --accent: #28695c;
      --accent-ink: #ffffff;
      --danger: #9c2f20;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      color: var(--ink);
      background: #fbfbf8;
    }
    main {
      width: min(860px, calc(100vw - 32px));
      margin: 48px auto;
    }
    header {
      display: flex;
      align-items: end;
      justify-content: space-between;
      gap: 24px;
      margin-bottom: 24px;
      border-bottom: 1px solid var(--line);
      padding-bottom: 18px;
    }
    h1 {
      margin: 0;
      font-size: 30px;
      line-height: 1.1;
      letter-spacing: 0;
    }
    .status {
      color: var(--muted);
      font-size: 14px;
      white-space: nowrap;
    }
    form {
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 22px;
      display: grid;
      gap: 18px;
    }
    label {
      display: grid;
      gap: 8px;
      font-weight: 700;
      font-size: 14px;
    }
    input[type="text"], input[type="file"] {
      width: 100%;
      min-height: 44px;
      border: 1px solid #bcc7bf;
      border-radius: 6px;
      background: #ffffff;
      color: var(--ink);
      padding: 10px 12px;
      font: inherit;
      font-weight: 500;
    }
    input[type="file"] { padding: 8px; }
    button {
      width: fit-content;
      min-height: 44px;
      border: 0;
      border-radius: 6px;
      background: var(--accent);
      color: var(--accent-ink);
      padding: 0 18px;
      font: inherit;
      font-weight: 800;
      cursor: pointer;
    }
    button:hover { filter: brightness(0.95); }
    .result, .error {
      margin-top: 18px;
      border-radius: 8px;
      padding: 16px;
      border: 1px solid var(--line);
      background: #ffffff;
    }
    .error {
      border-color: #d6a59c;
      color: var(--danger);
      background: #fff8f6;
      white-space: pre-wrap;
    }
    .result a {
      color: var(--accent);
      font-weight: 800;
    }
    .hint {
      color: var(--muted);
      font-size: 13px;
      font-weight: 500;
      line-height: 1.4;
    }
    @media (max-width: 620px) {
      main { margin: 24px auto; }
      header { display: block; }
      .status { margin-top: 8px; white-space: normal; }
      button { width: 100%; }
    }
  </style>
</head>
<body>
<main>
  <header>
    <h1>Finli Live</h1>
    <div class="status">listening on {{.Addr}}</div>
  </header>

  <form method="post" action="/jobs" enctype="multipart/form-data">
    <label>
      Invoice name
      <input name="invoice_name" type="text" placeholder="END OF MONTH INVOICE #3" required>
    </label>
    <label>
      Receipt directory
      <input name="receipts" type="file" webkitdirectory directory multiple required>
      <span class="hint">Files must be PDFs named for finli. Uploaded directories cannot contain nested folders.</span>
    </label>
    <button type="submit">Generate invoice PDF</button>
  </form>

  {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
  {{if .JobID}}
    <div class="result">
      PDFs ready:
      <a href="/jobs/{{.JobID}}/southroads/download">download {{.SouthroadsFileName}}</a>
      and
      <a href="/jobs/{{.JobID}}/utica/download">download {{.UticaFileName}}</a>
    </div>
  {{end}}
</main>
</body>
</html>`))

type viewData struct {
	Addr               string
	Error              string
	JobID              string
	SouthroadsFileName string
	UticaFileName      string
}

func main() {
	addr := appAddr()
	if _, err := exec.LookPath("finli"); err != nil {
		log.Fatalf("finli is not installed or is not on PATH. Install it from github.com/phillip-england/finli")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatalf("failed to create %s: %v", dataDir, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", indexHandler(addr))
	mux.HandleFunc("/jobs", jobsHandler(addr))
	mux.HandleFunc("/jobs/", downloadHandler)

	log.Printf("finli-live listening on http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func appAddr() string {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "9876"
	}
	return ":" + strings.TrimPrefix(port, ":")
}

func indexHandler(addr string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		render(w, http.StatusOK, viewData{Addr: addr})
	}
}

func jobsHandler(addr string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		job, err := runJob(w, r)
		if err != nil {
			render(w, http.StatusBadRequest, viewData{Addr: addr, Error: err.Error()})
			return
		}

		render(w, http.StatusOK, viewData{
			Addr:               addr,
			JobID:              job.id,
			SouthroadsFileName: filepath.Base(job.southroadsPDF),
			UticaFileName:      filepath.Base(job.uticaPDF),
		})
		return
	}
}

func downloadHandler(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "jobs" || parts[3] != "download" {
		http.NotFound(w, r)
		return
	}

	jobID := filepath.Base(parts[1])
	location := filepath.Base(parts[2])
	if location != "southroads" && location != "utica" {
		http.NotFound(w, r)
		return
	}

	fileName := location + ".pdf"
	path := filepath.Join(dataDir, jobID, fileName)
	if _, err := os.Stat(path); err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="finli-%s-invoice.pdf"`, location))
	http.ServeFile(w, r, path)
}

func render(w http.ResponseWriter, status int, data viewData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.WriteHeader(status)
	if err := page.Execute(w, data); err != nil {
		log.Printf("template render failed: %v", err)
	}
}

type jobResult struct {
	id            string
	southroadsPDF string
	uticaPDF      string
}

func runJob(w http.ResponseWriter, r *http.Request) (jobResult, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		return jobResult{}, fmt.Errorf("UPLOAD FAILURE: %w", err)
	}

	invoiceName := strings.TrimSpace(r.FormValue("invoice_name"))
	if invoiceName == "" {
		return jobResult{}, errors.New("MISSING INVOICE NAME: provide a name for the generated invoice")
	}
	if strings.ContainsAny(invoiceName, `/\`) {
		return jobResult{}, errors.New("INVALID INVOICE NAME: invoice names cannot contain path separators")
	}

	files := r.MultipartForm.File["receipts"]
	if len(files) == 0 {
		return jobResult{}, errors.New("MISSING RECEIPTS: upload a directory of finli PDF receipts")
	}

	jobID, err := randomID()
	if err != nil {
		return jobResult{}, err
	}
	jobRoot, err := filepath.Abs(filepath.Join(dataDir, jobID))
	if err != nil {
		return jobResult{}, fmt.Errorf("JOB SETUP FAILURE: failed to resolve job path: %w", err)
	}
	uploadDir := filepath.Join(jobRoot, "upload")
	sortedDir := filepath.Join(jobRoot, "sorted")
	workDir := filepath.Join(jobRoot, "work")
	for _, dir := range []string{uploadDir, sortedDir, workDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return jobResult{}, fmt.Errorf("JOB SETUP FAILURE: failed to create %s: %w", dir, err)
		}
	}

	if err := saveUploads(files, uploadDir); err != nil {
		return jobResult{}, err
	}

	if err := runFinli(jobRoot, "sort", uploadDir, sortedDir); err != nil {
		return jobResult{}, err
	}

	locationParts, err := generateLocationInvoices(sortedDir, workDir, invoiceName)
	if err != nil {
		return jobResult{}, err
	}

	southroadsPDF := filepath.Join(jobRoot, "southroads.pdf")
	if err := mergePDFs(southroadsPDF, locationParts["southroads"]); err != nil {
		return jobResult{}, err
	}

	uticaPDF := filepath.Join(jobRoot, "utica.pdf")
	if err := mergePDFs(uticaPDF, locationParts["utica"]); err != nil {
		return jobResult{}, err
	}

	return jobResult{id: jobID, southroadsPDF: southroadsPDF, uticaPDF: uticaPDF}, nil
}

func generateLocationInvoices(sortedDir, workDir, invoiceName string) (map[string][]string, error) {
	type location struct {
		dirSuffix   string
		titleSuffix string
	}
	locations := []location{
		{dirSuffix: "southroads", titleSuffix: "SOUTHROADS"},
		{dirSuffix: "utica", titleSuffix: "UTICA"},
	}

	locationParts := make(map[string][]string, len(locations))
	for _, loc := range locations {
		receiptDir := filepath.Join(sortedDir, loc.dirSuffix)
		receiptPDFs, err := pdfFiles(receiptDir)
		if err != nil {
			return nil, err
		}
		if len(receiptPDFs) == 0 {
			return nil, fmt.Errorf("SORT FAILURE: finli produced no %s receipt PDFs", loc.titleSuffix)
		}

		locationWorkDir := filepath.Join(workDir, loc.dirSuffix)
		if err := os.MkdirAll(locationWorkDir, 0o755); err != nil {
			return nil, fmt.Errorf("JOB SETUP FAILURE: failed to create %s: %w", locationWorkDir, err)
		}

		locationInvoiceName := invoiceName + " " + loc.titleSuffix
		if err := runFinli(locationWorkDir, "generate", receiptDir, locationInvoiceName); err != nil {
			return nil, err
		}

		invoicePDF := filepath.Join(locationWorkDir, invoiceFileName(locationInvoiceName))
		if _, err := os.Stat(invoicePDF); err != nil {
			return nil, fmt.Errorf("INVOICE OUTPUT FAILURE: expected finli to create %s", filepath.Base(invoicePDF))
		}

		parts := []string{invoicePDF}
		parts = append(parts, receiptPDFs...)
		locationParts[loc.dirSuffix] = parts
	}

	return locationParts, nil
}

func saveUploads(files []*multipart.FileHeader, dst string) error {
	for _, fileHeader := range files {
		rel, err := cleanUploadPath(fileHeader.Filename)
		if err != nil {
			return err
		}

		target := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("UPLOAD FAILURE: failed to create directory for %s: %w", rel, err)
		}

		src, err := fileHeader.Open()
		if err != nil {
			return fmt.Errorf("UPLOAD FAILURE: failed to open %s: %w", rel, err)
		}
		err = writeUpload(target, src)
		closeErr := src.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return fmt.Errorf("UPLOAD FAILURE: failed to close %s: %w", rel, closeErr)
		}
	}
	return nil
}

func cleanUploadPath(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimPrefix(name, "/")
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || clean == ".." {
		return "", fmt.Errorf("UPLOAD FAILURE: invalid upload path %q", name)
	}
	if strings.Count(filepath.ToSlash(clean), "/") > 1 {
		return "", fmt.Errorf("INVALID DIR CONTENTS: uploaded receipt directory must not contain nested subdirectories: %s", name)
	}
	if strings.ToLower(filepath.Ext(clean)) != ".pdf" {
		return "", fmt.Errorf("INVALID FILE EXTENSION: uploaded directory must contain only .pdf files: %s", name)
	}
	return filepath.Base(clean), nil
}

func writeUpload(target string, src multipart.File) error {
	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("UPLOAD FAILURE: failed to create %s: %w", filepath.Base(target), err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("UPLOAD FAILURE: failed to write %s: %w", filepath.Base(target), err)
	}
	return nil
}

func runFinli(dir string, args ...string) error {
	cmd := exec.Command("finli", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("FINLI FAILURE: finli %s failed\n%s", strings.Join(args, " "), strings.TrimSpace(out.String()))
	}
	return nil
}

func pdfFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if strings.ToLower(filepath.Ext(path)) == ".pdf" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("PDF DISCOVERY FAILURE: %w", err)
	}
	sort.Strings(files)
	return files, nil
}

func mergePDFs(out string, files []string) error {
	if len(files) == 0 {
		return errors.New("PDF MERGE FAILURE: no PDF files were provided")
	}
	conf := model.NewDefaultConfiguration()
	if err := api.MergeCreateFile(files, out, false, conf); err != nil {
		return fmt.Errorf("PDF MERGE FAILURE: %w", err)
	}
	return nil
}

func invoiceFileName(name string) string {
	return strings.ReplaceAll(strings.ToLower(name), " ", "_") + ".pdf"
}

func randomID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("JOB SETUP FAILURE: failed to allocate job id: %w", err)
	}
	return time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:]), nil
}
