package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	_ "modernc.org/sqlite"
)

const (
	maxUpload       = 512 << 20
	configPath      = "config/.env"
	defaultDBPath   = "data/main.sqlite"
	jobsDir         = "data/jobs"
	sessionCookie   = "finli_admin_session"
	sessionLifetime = 12 * time.Hour
	loginWindow     = 24 * time.Hour
	maxFailures     = 5
)

var publicPage = template.Must(template.New("public").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Finli Live</title>
  <style>{{template "style"}}</style>
</head>
<body>
<main>
  <header>
    <h1>Finli Live</h1>
    <nav>
      <a href="/">Overview</a>
      <a href="https://github.com/phillip-england/finli-live">GitHub</a>
    </nav>
  </header>

  <section class="intro">
    <p class="eyebrow">Invoice assembly for finli receipts</p>
    <h2>Turn a receipt folder into ready-to-send invoice packets.</h2>
    <p>Finli Live is the browser interface for the finli invoice workflow. It takes a flat folder of PDF receipts, runs the same sorting and generation steps as the command line, and returns complete location-specific PDF packets for download.</p>
    <div class="cta-row">
      <a class="button-link" href="https://github.com/phillip-england/finli-live">Try it on GitHub</a>
      <a href="#workflow">See how it works</a>
    </div>
  </section>

  <section class="process-visual" aria-label="Finli Live workflow">
    <div><span>1</span><strong>Upload PDFs</strong></div>
    <div><span>2</span><strong>Sort receipts</strong></div>
    <div><span>3</span><strong>Build invoices</strong></div>
    <div><span>4</span><strong>Download packets</strong></div>
  </section>

  <section class="band" id="workflow">
    <div class="section-heading">
      <p class="eyebrow">How the app works</p>
      <h3>One focused workflow runs the full invoice process.</h3>
    </div>
    <div class="steps">
      <article>
        <span class="step-number">01</span>
        <h4>Prepare the receipt folder</h4>
        <p>Collect the receipt PDFs that belong in the invoice batch. Keep them in one folder with no nested folders, and make sure the files are PDFs named in the format expected by finli.</p>
      </article>
      <article>
        <span class="step-number">02</span>
        <h4>Name the invoice batch</h4>
        <p>Enter the invoice name exactly as it should appear on the generated invoice pages, such as an end-of-month invoice label.</p>
      </article>
      <article>
        <span class="step-number">03</span>
        <h4>Upload the directory</h4>
        <p>Choose the full receipt directory in the browser. Finli Live accepts the files, rejects nested folders, and stores the job in a temporary job directory while it runs.</p>
      </article>
      <article>
        <span class="step-number">04</span>
        <h4>Download the finished PDFs</h4>
        <p>When processing completes, each selected location gets a downloadable PDF packet with the generated invoice followed by its matching receipts.</p>
      </article>
    </div>
  </section>

  <section class="band">
    <div class="section-heading">
      <p class="eyebrow">What Finli Live does for you</p>
      <h3>It wraps the command-line workflow in a focused web tool.</h3>
    </div>
    <div class="grid">
      <div><strong>Sorts receipts by location</strong><span>Runs finli sorting so uploaded PDFs are separated into the selected location groups.</span></div>
      <div><strong>Generates invoice pages</strong><span>Creates the location-specific invoice cover pages using the supplied invoice name.</span></div>
      <div><strong>Merges final packets</strong><span>Combines each invoice page with its sorted receipt PDFs into a single downloadable document.</span></div>
      <div><strong>Supports location targeting</strong><span>Uses locations.json and split targets to generate every location or only a selected subset.</span></div>
      <div><strong>Keeps jobs organized</strong><span>Stores each upload in its own job directory so generated location files stay tied to the correct batch.</span></div>
      <div><strong>Reports errors clearly</strong><span>Shows upload, sorting, generation, and merge failures so the receipt set can be corrected.</span></div>
    </div>
  </section>

  <section class="band split">
    <div>
      <p class="eyebrow">Before you upload</p>
      <h3>Receipt folder checklist</h3>
      <ul>
        <li>Use one flat folder only; do not include subfolders.</li>
        <li>Upload PDF receipts only.</li>
        <li>Use filenames that finli can recognize and sort.</li>
        <li>Remove duplicate, unrelated, or unfinished receipt files before generating the packet.</li>
      </ul>
    </div>
    <div>
      <p class="eyebrow">What you get back</p>
      <h3>Generated outputs</h3>
      <ul>
        <li>One PDF packet for each selected location.</li>
        <li>Use split for every location in locations.json.</li>
        <li>Each packet starts with its generated invoice page.</li>
        <li>The matching receipt PDFs are merged after the invoice page.</li>
      </ul>
    </div>
  </section>

  <section class="band guide">
    <div class="section-heading">
      <p class="eyebrow">Using the workflow</p>
      <h3>The workflow is intentionally short.</h3>
    </div>
    <ol>
      <li><strong>Enter the invoice name</strong> in the text field. This name is passed into the invoice generation step.</li>
      <li><strong>Select the receipt directory</strong> with the folder upload control. The browser will include every PDF in that folder and the optional locations.json file.</li>
      <li><strong>Choose the location target.</strong> Use split for all locations, or split-Southroads-Utica for a specific subset.</li>
      <li><strong>Click Generate invoice PDF.</strong> Wait for the job to finish before leaving the page.</li>
      <li><strong>Download each packet</strong> from the links shown after a successful run.</li>
    </ol>
  </section>

  <section class="band callout">
    <div>
      <p class="eyebrow">Open source</p>
      <h3>Install the project locally and adapt it to your receipt workflow.</h3>
    </div>
    <a class="button-link" href="https://github.com/phillip-england/finli-live">View on GitHub</a>
  </section>

  <footer>
    Made with ❤️ by <a href="https://phillip-england.com">Phillip England</a>
  </footer>
</main>
</body>
</html>`))

var adminPage = template.Must(template.New("admin").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Finli Live Admin</title>
  <style>{{template "style"}}</style>
</head>
<body>
<main>
  <header>
    <h1>Finli Live Admin</h1>
    <nav>
      <a href="/">Overview</a>
      <a href="/admin/locations">Locations JSON</a>
      <form class="inline" method="post" action="/logout"><button type="submit" class="link-button">Logout</button></form>
    </nav>
  </header>

  <form method="post" action="/admin/jobs" enctype="multipart/form-data">
    <label>
      Invoice name
      <input name="invoice_name" type="text" placeholder="END OF MONTH INVOICE #3" required>
    </label>
    <label>
      Receipt directory
      <input name="receipts" type="file" webkitdirectory directory multiple required>
      <span class="hint">Files must be PDFs named for finli. You may also include one top-level locations.json file. Uploaded directories cannot contain nested folders.</span>
    </label>
    <label>
      Location target
      <input name="location_target" type="text" value="split" required>
      <span class="hint">Use split for every location in locations.json, or split-Southroads-Utica to generate only those locations.</span>
    </label>
    <button type="submit">Generate invoice PDF</button>
  </form>

  {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
  {{if .JobID}}
    <div class="result">
      PDFs ready:
      {{range .Outputs}}
        <a href="/admin/jobs/{{$.JobID}}/{{.Slug}}/download">download {{.FileName}}</a>
      {{end}}
    </div>
  {{end}}
</main>
</body>
</html>`))

var locationsPage = template.Must(template.New("locations").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Finli Live Locations</title>
  <style>{{template "style"}}</style>
</head>
<body>
<main>
  <header>
    <h1>Locations JSON</h1>
    <nav>
      <a href="/admin">Admin</a>
      <form class="inline" method="post" action="/logout"><button type="submit" class="link-button">Logout</button></form>
    </nav>
  </header>

  <form method="post" action="/admin/locations">
    <label>
      Locations
      <textarea name="locations" rows="10" required>Southroads
Utica</textarea>
      <span class="hint">Enter one receipt location per line. The downloaded file should be included in the top level of the receipt directory upload.</span>
    </label>
    <button type="submit">Download locations.json</button>
  </form>

  {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
</main>
</body>
</html>`))

var loginPage = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Finli Live Login</title>
  <style>{{template "style"}}</style>
</head>
<body>
<main class="narrow">
  <header>
    <h1>Admin Login</h1>
    <nav><a href="/">Overview</a></nav>
  </header>

  <form method="post" action="/login">
    <label>
      Username
      <input name="username" type="text" autocomplete="username" required>
    </label>
    <label>
      Password
      <input name="password" type="password" autocomplete="current-password" required>
    </label>
    <button type="submit">Log in</button>
  </form>

  {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
</main>
</body>
</html>`))

func init() {
	style := template.Must(template.New("style").Parse(`{{define "style"}}
    :root {
      color-scheme: light;
      --ink: #17201a;
      --muted: #68746d;
      --line: #d8ded9;
      --panel: #f7f8f5;
      --accent: #28695c;
      --accent-ink: #ffffff;
      --danger: #9c2f20;
      --warn-bg: #fff8f6;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      color: var(--ink);
      background: #fbfbf8;
    }
    main {
      width: min(900px, calc(100vw - 32px));
      margin: 48px auto;
    }
    main.narrow { width: min(520px, calc(100vw - 32px)); }
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
    h2 {
      max-width: 720px;
      margin: 0;
      font-size: 34px;
      line-height: 1.12;
      letter-spacing: 0;
    }
    h3 {
      margin: 0;
      font-size: 24px;
      line-height: 1.2;
      letter-spacing: 0;
    }
    h4 {
      margin: 10px 0 8px;
      font-size: 17px;
      line-height: 1.25;
      letter-spacing: 0;
    }
    p { color: var(--muted); font-size: 16px; line-height: 1.6; }
    nav { display: flex; align-items: center; gap: 14px; }
    a, .link-button {
      color: var(--accent);
      font: inherit;
      font-weight: 800;
      text-decoration: none;
    }
    a:hover, .link-button:hover { text-decoration: underline; }
    .inline { display: inline; padding: 0; border: 0; background: transparent; }
    .link-button {
      width: auto;
      min-height: auto;
      padding: 0;
      border: 0;
      background: transparent;
      cursor: pointer;
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
    input[type="text"], input[type="password"], input[type="file"], textarea {
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
    textarea { resize: vertical; }
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
    .button-link {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      min-height: 44px;
      border-radius: 6px;
      background: var(--accent);
      color: var(--accent-ink);
      padding: 0 18px;
      text-decoration: none;
    }
    .button-link:hover {
      filter: brightness(0.95);
      text-decoration: none;
    }
    .intro {
      display: grid;
      gap: 14px;
      padding: 10px 0 24px;
    }
    .intro p { max-width: 690px; margin: 0; }
    .cta-row {
      display: flex;
      align-items: center;
      gap: 18px;
      flex-wrap: wrap;
      margin-top: 4px;
    }
    .eyebrow {
      color: var(--accent);
      font-size: 13px;
      font-weight: 800;
      letter-spacing: 0;
      text-transform: uppercase;
    }
    .process-visual {
      display: grid;
      grid-template-columns: repeat(4, minmax(0, 1fr));
      gap: 1px;
      overflow: hidden;
      border: 1px solid var(--line);
      border-radius: 8px;
      background: var(--line);
      margin: 8px 0 28px;
    }
    .process-visual div {
      min-height: 120px;
      display: grid;
      align-content: space-between;
      gap: 20px;
      background: linear-gradient(135deg, #ffffff 0%, #eef4ef 100%);
      padding: 18px;
    }
    .process-visual span {
      width: 34px;
      height: 34px;
      display: inline-grid;
      place-items: center;
      border-radius: 50%;
      background: var(--accent);
      color: var(--accent-ink);
      font-weight: 900;
      font-size: 14px;
    }
    .process-visual strong {
      display: block;
      font-size: 16px;
      line-height: 1.25;
    }
    .band {
      border-top: 1px solid var(--line);
      padding-top: 28px;
      margin-top: 28px;
    }
    .section-heading {
      display: grid;
      gap: 8px;
      margin-bottom: 18px;
    }
    .grid {
      display: grid;
      grid-template-columns: repeat(3, minmax(0, 1fr));
      gap: 16px;
    }
    .grid div, .steps article, .result, .error {
      border-radius: 8px;
      padding: 16px;
      border: 1px solid var(--line);
      background: #ffffff;
    }
    .grid strong, .grid span { display: block; }
    .grid span {
      margin-top: 8px;
      color: var(--muted);
      font-size: 14px;
      line-height: 1.45;
    }
    .steps {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 16px;
    }
    .steps p {
      margin: 0;
      font-size: 14px;
      line-height: 1.5;
    }
    .step-number {
      color: var(--accent);
      font-weight: 900;
      font-size: 13px;
    }
    .split {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 32px;
    }
    ul, ol {
      margin: 14px 0 0;
      padding-left: 20px;
      color: var(--muted);
      line-height: 1.6;
    }
    li { margin: 8px 0; }
    li strong { color: var(--ink); }
    .guide ol {
      max-width: 760px;
    }
    .callout {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 20px;
      padding-bottom: 4px;
    }
    .callout .eyebrow { margin: 0 0 8px; }
    footer {
      margin-top: 36px;
      padding-top: 18px;
      border-top: 1px solid var(--line);
      color: var(--muted);
      font-size: 14px;
      line-height: 1.5;
    }
    .result, .error { margin-top: 18px; }
    .error {
      border-color: #d6a59c;
      color: var(--danger);
      background: var(--warn-bg);
      white-space: pre-wrap;
    }
    .hint {
      color: var(--muted);
      font-size: 13px;
      font-weight: 500;
      line-height: 1.4;
    }
    @media (max-width: 720px) {
      main { margin: 24px auto; }
      header { display: block; }
      nav { margin-top: 12px; flex-wrap: wrap; }
      h2 { font-size: 28px; }
      button { width: 100%; }
      .button-link { width: 100%; }
      .link-button { width: auto; }
      .process-visual, .grid, .steps, .split { grid-template-columns: 1fr; }
      .process-visual div { min-height: 96px; }
      .cta-row, .callout { display: grid; }
    }
  {{end}}`))
	for _, page := range []*template.Template{publicPage, adminPage, locationsPage, loginPage} {
		if _, err := page.AddParseTree("style", style.Lookup("style").Tree); err != nil {
			panic(err)
		}
	}
}

type appConfig struct {
	Port          string
	AdminUser     string
	AdminPassword string
	SessionSecret []byte
	DBPath        string
	TrustProxy    bool
}

type app struct {
	cfg      appConfig
	db       *sql.DB
	sessions *sessionStore
}

type viewData struct {
	Error   string
	JobID   string
	Outputs []jobOutput
}

type jobOutput struct {
	Slug     string
	FileName string
	Path     string
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	if _, err := exec.LookPath("finli"); err != nil {
		log.Fatalf("finli is not installed or is not on PATH. Install it from github.com/phillip-england/finli")
	}
	if err := os.MkdirAll(jobsDir, 0o755); err != nil {
		log.Fatalf("failed to create %s: %v", jobsDir, err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		log.Fatalf("failed to create database directory: %v", err)
	}

	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		log.Fatalf("failed to open sqlite database: %v", err)
	}
	defer db.Close()
	if err := initDB(db); err != nil {
		log.Fatalf("failed to initialize sqlite database: %v", err)
	}

	application := &app{cfg: cfg, db: db, sessions: newSessionStore()}
	mux := http.NewServeMux()
	mux.HandleFunc("/", application.publicHandler)
	mux.HandleFunc("/login", application.loginHandler)
	mux.HandleFunc("/logout", application.logoutHandler)
	mux.Handle("/admin", application.requireAdmin(http.HandlerFunc(application.adminHandler)))
	mux.Handle("/admin/", application.requireAdmin(http.HandlerFunc(application.adminRoutes)))

	addr := "0.0.0.0:" + cfg.Port
	log.Printf("finli-live listening on http://%s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func loadConfig() (appConfig, error) {
	values, err := readEnvFile(configPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return appConfig{}, err
	}

	get := func(key string) string {
		if value, ok := os.LookupEnv(key); ok {
			return strings.TrimSpace(value)
		}
		return strings.TrimSpace(values[key])
	}

	port := get("PORT")
	if port == "" {
		port = "9876"
	}
	user := get("ADMIN_USERNAME")
	password := get("ADMIN_PASSWORD")
	secret := get("SESSION_SECRET")
	if user == "" || password == "" || secret == "" {
		return appConfig{}, fmt.Errorf("ADMIN_USERNAME, ADMIN_PASSWORD, and SESSION_SECRET must be set in %s or the environment", configPath)
	}

	dbPath := get("DB_PATH")
	if dbPath == "" {
		dbPath = defaultDBPath
	}
	if strings.HasPrefix(dbPath, ".."+string(os.PathSeparator)) || strings.HasPrefix(dbPath, "../") {
		dbPath = filepath.Clean(filepath.Join(filepath.Dir(configPath), dbPath))
	}

	trustProxy, err := strconv.ParseBool(defaultString(get("TRUST_PROXY"), "false"))
	if err != nil {
		return appConfig{}, fmt.Errorf("TRUST_PROXY must be true or false: %w", err)
	}

	return appConfig{
		Port:          strings.TrimPrefix(port, ":"),
		AdminUser:     user,
		AdminPassword: password,
		SessionSecret: []byte(secret),
		DBPath:        dbPath,
		TrustProxy:    trustProxy,
	}, nil
}

func readEnvFile(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	values := make(map[string]string)
	lines := strings.Split(string(b), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return values, nil
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func initDB(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS login_failures (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ip TEXT NOT NULL,
  attempted_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_login_failures_ip_time
ON login_failures (ip, attempted_at);
`)
	return err
}

func (a *app) publicHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	renderTemplate(w, http.StatusOK, publicPage, viewData{})
}

func (a *app) loginHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if a.currentSession(r) != "" {
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}
		renderTemplate(w, http.StatusOK, loginPage, viewData{})
	case http.MethodPost:
		a.handleLogin(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *app) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := a.clientIP(r)
	now := time.Now().Unix()
	cutoff := time.Now().Add(-loginWindow).Unix()
	if _, err := a.db.Exec(`DELETE FROM login_failures WHERE attempted_at < ?`, cutoff); err != nil {
		http.Error(w, "login unavailable", http.StatusInternalServerError)
		log.Printf("failed to prune login failures: %v", err)
		return
	}

	recent, err := a.failureCount(ip, cutoff)
	if err != nil {
		http.Error(w, "login unavailable", http.StatusInternalServerError)
		log.Printf("failed to count login failures: %v", err)
		return
	}
	if recent >= maxFailures {
		http.Error(w, "too many login attempts", http.StatusForbidden)
		return
	}

	if a.validCredentials(r.FormValue("username"), r.FormValue("password")) {
		id, err := randomToken(32)
		if err != nil {
			http.Error(w, "login unavailable", http.StatusInternalServerError)
			log.Printf("failed to create session id: %v", err)
			return
		}
		a.sessions.create(id, time.Now().Add(sessionLifetime))
		http.SetCookie(w, a.signedCookie(id, time.Now().Add(sessionLifetime)))
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	if _, err := a.db.Exec(`INSERT INTO login_failures (ip, attempted_at) VALUES (?, ?)`, ip, now); err != nil {
		http.Error(w, "login unavailable", http.StatusInternalServerError)
		log.Printf("failed to record login failure: %v", err)
		return
	}
	recent, err = a.failureCount(ip, cutoff)
	if err != nil {
		http.Error(w, "login unavailable", http.StatusInternalServerError)
		log.Printf("failed to recount login failures: %v", err)
		return
	}
	if recent >= maxFailures {
		http.Error(w, "too many login attempts", http.StatusForbidden)
		return
	}
	renderTemplate(w, http.StatusUnauthorized, loginPage, viewData{Error: "Invalid username or password."})
}

func (a *app) failureCount(ip string, cutoff int64) (int, error) {
	var count int
	err := a.db.QueryRow(`SELECT COUNT(*) FROM login_failures WHERE ip = ? AND attempted_at >= ?`, ip, cutoff).Scan(&count)
	return count, err
}

func (a *app) validCredentials(username, password string) bool {
	userOK := subtle.ConstantTimeCompare([]byte(username), []byte(a.cfg.AdminUser)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(password), []byte(a.cfg.AdminPassword)) == 1
	return userOK && passOK
}

func (a *app) logoutHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if id, ok := a.verifyCookie(cookie.Value); ok {
			a.sessions.delete(id)
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *app) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.currentSession(r) == "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *app) currentSession(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	id, ok := a.verifyCookie(cookie.Value)
	if !ok || !a.sessions.valid(id) {
		return ""
	}
	return id
}

func (a *app) signedCookie(id string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookie,
		Value:    id + "." + a.signature(id),
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(sessionLifetime.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

func (a *app) verifyCookie(value string) (string, bool) {
	id, sig, ok := strings.Cut(value, ".")
	if !ok || id == "" || sig == "" {
		return "", false
	}
	expected := a.signature(id)
	if hmac.Equal([]byte(sig), []byte(expected)) {
		return id, true
	}
	return "", false
}

func (a *app) signature(id string) string {
	mac := hmac.New(sha256.New, a.cfg.SessionSecret)
	mac.Write([]byte(id))
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *app) clientIP(r *http.Request) string {
	if a.cfg.TrustProxy {
		if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
			first, _, _ := strings.Cut(forwarded, ",")
			if ip := net.ParseIP(strings.TrimSpace(first)); ip != nil {
				return ip.String()
			}
		}
		if realIP := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); realIP != nil {
			return realIP.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (a *app) adminHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	renderTemplate(w, http.StatusOK, adminPage, viewData{})
}

func (a *app) adminRoutes(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/admin/jobs":
		a.jobsHandler(w, r)
	case r.URL.Path == "/admin/locations":
		a.locationsHandler(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/jobs/"):
		a.downloadHandler(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (a *app) jobsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	job, err := runJob(w, r)
	if err != nil {
		renderTemplate(w, http.StatusBadRequest, adminPage, viewData{Error: err.Error()})
		return
	}

	renderTemplate(w, http.StatusOK, adminPage, viewData{
		JobID:   job.id,
		Outputs: job.outputs,
	})
}

func (a *app) locationsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		renderTemplate(w, http.StatusOK, locationsPage, viewData{})
	case http.MethodPost:
		locations, err := parseLocationLines(r.FormValue("locations"))
		if err != nil {
			renderTemplate(w, http.StatusBadRequest, locationsPage, viewData{Error: err.Error()})
			return
		}
		payload, err := json.MarshalIndent(locationConfig{Locations: locationNames(locations)}, "", "  ")
		if err != nil {
			http.Error(w, "failed to create locations.json", http.StatusInternalServerError)
			log.Printf("locations json render failed: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="locations.json"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(append(payload, '\n'))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *app) downloadHandler(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 5 || parts[0] != "admin" || parts[1] != "jobs" || parts[4] != "download" {
		http.NotFound(w, r)
		return
	}

	jobID := filepath.Base(parts[2])
	location := filepath.Base(parts[3])
	if !validLocationSlug(location) {
		http.NotFound(w, r)
		return
	}

	path := filepath.Join(jobsDir, jobID, location+".pdf")
	if _, err := os.Stat(path); err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="finli-%s-invoice.pdf"`, location))
	http.ServeFile(w, r, path)
}

func renderTemplate(w http.ResponseWriter, status int, tmpl *template.Template, data viewData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.WriteHeader(status)
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("template render failed: %v", err)
	}
}

type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]time.Time
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: make(map[string]time.Time)}
}

func (s *sessionStore) create(id string, expires time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = expires
}

func (s *sessionStore) valid(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	expires, ok := s.sessions[id]
	if !ok {
		return false
	}
	if time.Now().After(expires) {
		delete(s.sessions, id)
		return false
	}
	return true
}

func (s *sessionStore) delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

type jobResult struct {
	id      string
	outputs []jobOutput
}

type locationSpec struct {
	Name        string
	Slug        string
	TitleSuffix string
}

type locationConfig struct {
	Locations []string `json:"locations"`
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

	locationTarget := strings.TrimSpace(r.FormValue("location_target"))
	if locationTarget == "" {
		locationTarget = "split"
	}

	files := r.MultipartForm.File["receipts"]
	if len(files) == 0 {
		return jobResult{}, errors.New("MISSING RECEIPTS: upload a directory of finli PDF receipts")
	}

	jobID, err := randomID()
	if err != nil {
		return jobResult{}, err
	}
	jobRoot, err := filepath.Abs(filepath.Join(jobsDir, jobID))
	if err != nil {
		return jobResult{}, fmt.Errorf("JOB SETUP FAILURE: failed to resolve job path: %w", err)
	}
	uploadDir := filepath.Join(jobRoot, "upload")
	sortedDir := filepath.Join(jobRoot, "sorted")
	workDir := filepath.Join(jobRoot, "work")
	locationsPath := filepath.Join(jobRoot, "locations.json")
	for _, dir := range []string{uploadDir, sortedDir, workDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return jobResult{}, fmt.Errorf("JOB SETUP FAILURE: failed to create %s: %w", dir, err)
		}
	}

	hasLocationsConfig, err := saveUploads(files, uploadDir, locationsPath)
	if err != nil {
		return jobResult{}, err
	}

	locations := defaultLocations()
	if hasLocationsConfig {
		locations, err = readLocationsFile(locationsPath)
		if err != nil {
			return jobResult{}, err
		}
	}
	selectedLocations, err := selectLocations(locations, locationTarget)
	if err != nil {
		return jobResult{}, err
	}

	if err := runFinli(jobRoot, "sort", uploadDir, sortedDir); err != nil {
		return jobResult{}, err
	}

	locationParts, err := generateLocationInvoices(sortedDir, workDir, invoiceName, selectedLocations)
	if err != nil {
		return jobResult{}, err
	}

	outputs := make([]jobOutput, 0, len(selectedLocations))
	for _, loc := range selectedLocations {
		outputPDF := filepath.Join(jobRoot, loc.Slug+".pdf")
		if err := mergePDFs(outputPDF, locationParts[loc.Slug]); err != nil {
			return jobResult{}, err
		}
		outputs = append(outputs, jobOutput{
			Slug:     loc.Slug,
			FileName: filepath.Base(outputPDF),
			Path:     outputPDF,
		})
	}

	return jobResult{id: jobID, outputs: outputs}, nil
}

func generateLocationInvoices(sortedDir, workDir, invoiceName string, locations []locationSpec) (map[string][]string, error) {
	locationParts := make(map[string][]string, len(locations))
	for _, loc := range locations {
		receiptDir := filepath.Join(sortedDir, loc.Slug)
		receiptPDFs, err := pdfFiles(receiptDir)
		if err != nil {
			return nil, err
		}
		if len(receiptPDFs) == 0 {
			return nil, fmt.Errorf("SORT FAILURE: finli produced no %s receipt PDFs", loc.TitleSuffix)
		}

		locationWorkDir := filepath.Join(workDir, loc.Slug)
		if err := os.MkdirAll(locationWorkDir, 0o755); err != nil {
			return nil, fmt.Errorf("JOB SETUP FAILURE: failed to create %s: %w", locationWorkDir, err)
		}

		locationInvoiceName := invoiceName + " " + loc.TitleSuffix
		if err := runFinli(locationWorkDir, "generate", receiptDir, locationInvoiceName); err != nil {
			return nil, err
		}

		invoicePDF := filepath.Join(locationWorkDir, invoiceFileName(locationInvoiceName))
		if _, err := os.Stat(invoicePDF); err != nil {
			return nil, fmt.Errorf("INVOICE OUTPUT FAILURE: expected finli to create %s", filepath.Base(invoicePDF))
		}

		parts := []string{invoicePDF}
		parts = append(parts, receiptPDFs...)
		locationParts[loc.Slug] = parts
	}

	return locationParts, nil
}

func saveUploads(files []*multipart.FileHeader, receiptsDir, locationsPath string) (bool, error) {
	hasLocationsConfig := false
	for _, fileHeader := range files {
		rel, err := cleanUploadPath(fileHeader.Filename)
		if err != nil {
			return false, err
		}

		target := filepath.Join(receiptsDir, rel)
		if strings.EqualFold(rel, "locations.json") {
			if hasLocationsConfig {
				return false, errors.New("INVALID LOCATIONS CONFIG: upload only one locations.json file")
			}
			hasLocationsConfig = true
			target = locationsPath
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return false, fmt.Errorf("UPLOAD FAILURE: failed to create directory for %s: %w", rel, err)
		}

		src, err := fileHeader.Open()
		if err != nil {
			return false, fmt.Errorf("UPLOAD FAILURE: failed to open %s: %w", rel, err)
		}
		err = writeUpload(target, src)
		closeErr := src.Close()
		if err != nil {
			return false, err
		}
		if closeErr != nil {
			return false, fmt.Errorf("UPLOAD FAILURE: failed to close %s: %w", rel, closeErr)
		}
	}
	return hasLocationsConfig, nil
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
	base := filepath.Base(clean)
	ext := strings.ToLower(filepath.Ext(clean))
	if ext != ".pdf" && !strings.EqualFold(base, "locations.json") {
		return "", fmt.Errorf("INVALID FILE EXTENSION: uploaded directory must contain only .pdf files and one optional locations.json file: %s", name)
	}
	return base, nil
}

func defaultLocations() []locationSpec {
	return []locationSpec{
		mustLocationSpec("Southroads"),
		mustLocationSpec("Utica"),
	}
}

func mustLocationSpec(name string) locationSpec {
	loc, err := newLocationSpec(name)
	if err != nil {
		panic(err)
	}
	return loc
}

func readLocationsFile(path string) ([]locationSpec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("LOCATIONS CONFIG FAILURE: failed to read locations.json: %w", err)
	}

	var cfg locationConfig
	if err := json.Unmarshal(b, &cfg); err != nil || len(cfg.Locations) == 0 {
		var names []string
		if arrayErr := json.Unmarshal(b, &names); arrayErr != nil {
			if err != nil {
				return nil, fmt.Errorf("LOCATIONS CONFIG FAILURE: locations.json must be an object with a locations array or a string array: %w", err)
			}
			return nil, fmt.Errorf("LOCATIONS CONFIG FAILURE: locations.json must be an object with a locations array or a string array: %w", arrayErr)
		}
		cfg.Locations = names
	}

	return parseLocationNames(cfg.Locations)
}

func parseLocationLines(value string) ([]locationSpec, error) {
	var names []string
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		names = append(names, line)
	}
	return parseLocationNames(names)
}

func parseLocationNames(names []string) ([]locationSpec, error) {
	if len(names) == 0 {
		return nil, errors.New("LOCATIONS CONFIG FAILURE: provide at least one location")
	}

	seen := make(map[string]struct{}, len(names))
	locations := make([]locationSpec, 0, len(names))
	for _, name := range names {
		loc, err := newLocationSpec(name)
		if err != nil {
			return nil, err
		}
		key := locationKey(loc.Name)
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("LOCATIONS CONFIG FAILURE: duplicate location %q", loc.Name)
		}
		seen[key] = struct{}{}
		locations = append(locations, loc)
	}
	return locations, nil
}

func locationNames(locations []locationSpec) []string {
	names := make([]string, 0, len(locations))
	for _, loc := range locations {
		names = append(names, loc.Name)
	}
	return names
}

func newLocationSpec(name string) (locationSpec, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return locationSpec{}, errors.New("LOCATIONS CONFIG FAILURE: location names cannot be blank")
	}
	slug := locationSlug(name)
	if slug == "" {
		return locationSpec{}, fmt.Errorf("LOCATIONS CONFIG FAILURE: location %q must include at least one letter or number", name)
	}
	return locationSpec{
		Name:        name,
		Slug:        slug,
		TitleSuffix: strings.ToUpper(name),
	}, nil
}

func selectLocations(locations []locationSpec, target string) ([]locationSpec, error) {
	target = strings.TrimSpace(target)
	if strings.EqualFold(target, "split") {
		return locations, nil
	}
	if !strings.HasPrefix(strings.ToLower(target), "split-") {
		return nil, errors.New("INVALID LOCATION TARGET: use split or split-Location-AnotherLocation")
	}

	byName := make(map[string]locationSpec, len(locations))
	for _, loc := range locations {
		byName[locationKey(loc.Name)] = loc
		byName[locationKey(loc.Slug)] = loc
	}

	var selected []locationSpec
	seen := make(map[string]struct{})
	for _, part := range strings.Split(target[len("split-"):], "-") {
		key := locationKey(part)
		if key == "" {
			continue
		}
		loc, ok := byName[key]
		if !ok {
			return nil, fmt.Errorf("INVALID LOCATION TARGET: %q is not listed in locations.json", part)
		}
		if _, ok := seen[loc.Slug]; ok {
			continue
		}
		seen[loc.Slug] = struct{}{}
		selected = append(selected, loc)
	}
	if len(selected) == 0 {
		return nil, errors.New("INVALID LOCATION TARGET: include at least one location after split-")
	}
	return selected, nil
}

func locationSlug(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func locationKey(name string) string {
	return strings.ReplaceAll(locationSlug(name), "-", "")
}

func validLocationSlug(slug string) bool {
	return slug != "" && slug == locationSlug(slug)
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
	id, err := randomToken(8)
	if err != nil {
		return "", fmt.Errorf("JOB SETUP FAILURE: failed to allocate job id: %w", err)
	}
	return time.Now().UTC().Format("20060102T150405Z") + "-" + id, nil
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
