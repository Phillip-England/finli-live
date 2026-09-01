package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
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
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jung-kurt/gofpdf"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	_ "modernc.org/sqlite"
)

const (
	maxUpload            = 512 << 20
	defaultDBPath        = "data/main.sqlite"
	jobsDir              = "data/jobs"
	usageWindow          = 24 * time.Hour
	jobTTL               = 24 * time.Hour
	maxUsesPerIP         = 10
	maxFormInvoicesPerIP = 25
	maxFormLineItems     = 100
	maxFormLocations     = 25
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
      <a href="#generate">Generate</a>
      <a href="/create">Form invoice</a>
      <a href="https://github.com/phillip-england/finli-live">GitHub</a>
    </nav>
  </header>

  <section class="intro">
    <p class="eyebrow">Filename-driven invoice generation</p>
    <h2>The invoice is generated from the receipt name itself.</h2>
    <p>Finli Live turns a flat folder of PDFs into location-specific invoice packets by parsing a strict six-part naming schema. The name carries the date, vendor, cost, description, expense category, and location, so the app can sort receipts, split shared purchases, calculate totals, build invoice pages, and merge the final PDFs without a spreadsheet.</p>
    <div class="cta-row">
      <a class="button-link" href="#generate">Generate invoices</a>
      <a href="#schema">Study the schema</a>
    </div>
  </section>

  <section class="schema-strip" id="schema" aria-label="Finli filename schema">
    <code>010125-target-10.95-pants-uniforms-southroads.pdf</code>
    <div class="schema-grid">
      <span>Date</span>
      <span>Vendor</span>
      <span>Cost</span>
      <span>Description</span>
      <span>Category</span>
      <span>Location</span>
    </div>
  </section>

  <section class="band split" id="generate">
    <div>
      <div class="section-heading">
        <p class="eyebrow">Public generator</p>
        <h3>Upload a folder and get invoice PDFs back.</h3>
      </div>
      <form method="post" action="/jobs" enctype="multipart/form-data">
        <label>
          Invoice name
          <input name="invoice_name" type="text" placeholder="END OF MONTH INVOICE #3" required>
        </label>
        <label>
          Receipt directory
          <input name="receipts" type="file" webkitdirectory directory multiple required>
          <span class="hint">Upload one flat folder containing only PDFs named with the Finli schema. Locations are inferred from the filename location field.</span>
        </label>
        <button type="submit">Generate invoice PDFs</button>
      </form>

      {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
      {{if .JobID}}
        <div class="result">
          <strong>PDFs ready</strong>
          {{range .Outputs}}
            <a href="/jobs/{{$.JobID}}/{{.Slug}}/download">download {{.FileName}}</a>
          {{end}}
        </div>
      {{end}}
    </div>
    <div class="rules-panel">
      <p class="eyebrow">Daily limit</p>
      <h3>10 generations per IP address every 24 hours.</h3>
      <p>The public generator keeps a short-lived usage ledger in SQLite and prunes entries older than 24 hours before each request, so rate-limit data stays bounded.</p>
    </div>
  </section>

  <section class="band">
    <div class="section-heading">
      <p class="eyebrow">Why the schema matters</p>
      <h3>Filenames become structured invoice data.</h3>
    </div>
    <div class="steps">
      <article>
        <span class="step-number">01</span>
        <h4>Date</h4>
        <p>Use six digits like 010125. This gives every invoice line a compact receipt date.</p>
      </article>
      <article>
        <span class="step-number">02</span>
        <h4>Vendor and amount</h4>
        <p>The vendor and exact decimal cost are parsed into line items and category totals.</p>
      </article>
      <article>
        <span class="step-number">03</span>
        <h4>Description and category</h4>
        <p>These fields explain what was bought and group related spending on the invoice page.</p>
      </article>
      <article>
        <span class="step-number">04</span>
        <h4>Location or split</h4>
        <p>A location routes the receipt to one packet. The special split location divides the amount across every inferred location.</p>
      </article>
    </div>
  </section>

  <section class="band">
    <div class="section-heading">
      <p class="eyebrow">Generated outputs</p>
      <h3>The uploaded folder becomes complete invoice packets.</h3>
    </div>
    <div class="grid">
      <div><strong>Sorts receipts by location</strong><span>Separates uploaded PDFs into inferred location groups and splits shared receipts accurately.</span></div>
      <div><strong>Generates invoice pages</strong><span>Creates the location-specific invoice cover pages using the supplied invoice name.</span></div>
      <div><strong>Merges final packets</strong><span>Combines each invoice page with its sorted receipt PDFs into a single downloadable document.</span></div>
      <div><strong>Infers locations automatically</strong><span>Scans the uploaded receipt names and generates one packet for every concrete location it finds.</span></div>
      <div><strong>Keeps jobs organized</strong><span>Stores each upload in its own job directory so generated location files stay tied to the correct batch.</span></div>
      <div><strong>Reports errors clearly</strong><span>Shows upload, sorting, generation, and merge failures so the receipt set can be corrected.</span></div>
    </div>
  </section>

  <section class="band split">
    <div>
      <p class="eyebrow">Receipt folder</p>
      <h3>Keep the upload intentionally simple.</h3>
      <ul>
        <li>Use one flat folder only; do not include subfolders.</li>
        <li>Upload PDF receipts only.</li>
        <li>Name every receipt as date-vendor-cost-description-category-location.pdf.</li>
        <li>Remove duplicate, unrelated, or unfinished receipt files before generating the packet.</li>
      </ul>
    </div>
    <div>
      <p class="eyebrow">Automatic locations</p>
      <h3>The folder tells Finli what to build.</h3>
      <ul>
        <li>Use a concrete location like southroads, utica, or downtown in receipt filenames.</li>
        <li>Use split only for receipts that should be divided across inferred locations.</li>
        <li>Include at least one non-split receipt so Finli can discover the location set.</li>
        <li>Every inferred location receives its own generated invoice packet.</li>
      </ul>
    </div>
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

var createPage = template.Must(template.New("create").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Create an invoice · Finli Live</title>
  <style>{{template "style"}}</style>
</head>
<body>
<main>
  <header>
    <h1><a class="brand" href="/">Finli Live</a></h1>
    <nav><a href="/">Folder generator</a><a class="active" href="/create">Form invoice</a></nav>
  </header>

  <section class="intro create-intro">
    <p class="eyebrow">Standalone invoice builder</p>
    <h2>Build one polished invoice, right in the browser.</h2>
    <p>Add as many line items as you need, define the locations paying for them, and assign each item to one or several locations. Finli divides split amounts down to the penny and creates one ready-to-send PDF.</p>
  </section>

  <div class="mode-note">
    <strong>No receipt folder?</strong>
    <span>This form creates a standalone invoice. If you have named receipt PDFs to sort and bundle, use the <a href="/">folder generator</a>.</span>
  </div>

  <form class="invoice-form" method="post" action="/invoices" id="invoice-form">
    <section class="form-section">
      <div class="form-section-heading"><span>01</span><div><h3>Invoice details</h3><p>Name the document and add optional context for the recipient.</p></div></div>
      <div class="field-grid">
        <label>Invoice title<input name="invoice_name" type="text" maxlength="100" placeholder="September operating expenses" required></label>
        <label>Invoice date<input name="invoice_date" type="date" required></label>
        <label>Bill to<input name="bill_to" type="text" maxlength="120" placeholder="Organization or contact"></label>
        <label>Reference<input name="reference" type="text" maxlength="80" placeholder="PO, account, or invoice number"></label>
      </div>
      <label>Notes<textarea name="notes" rows="3" maxlength="800" placeholder="Payment details or a short message (optional)"></textarea></label>
    </section>

    <section class="form-section">
      <div class="form-section-heading"><span>02</span><div><h3>Split locations</h3><p>Add every location that can share an expense. You can choose one or several per line item.</p></div></div>
      <div id="locations" class="location-list"></div>
      <button class="secondary-button" type="button" id="add-location">+ Add location</button>
    </section>

    <section class="form-section">
      <div class="form-section-heading"><span>03</span><div><h3>Line items</h3><p>Amounts assigned to several locations are divided evenly, with every cent accounted for.</p></div></div>
      <div id="line-items" class="line-items"></div>
      <button class="secondary-button" type="button" id="add-line">+ Add line item</button>
    </section>

    <div class="form-submit">
      <div><strong>25 invoices per IP every 24 hours</strong><span>Your generated file is available for 24 hours.</span></div>
      <button type="submit">Create standalone PDF</button>
    </div>
  </form>

  {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
  {{if .JobID}}<div class="result success-result"><strong>Your invoice is ready.</strong>{{range .Outputs}}<a class="button-link" href="/jobs/{{$.JobID}}/{{.Slug}}/download">Download {{.FileName}}</a>{{end}}<a href="/create">Create another invoice</a></div>{{end}}

  <footer>Made with ❤️ by <a href="https://phillip-england.com">Phillip England</a></footer>
</main>
<template id="location-template"><div class="location-row"><label>Location <input name="locations" type="text" maxlength="60" placeholder="e.g. Southroads" required></label><button class="remove-button" type="button" aria-label="Remove location">Remove</button></div></template>
<template id="line-template"><article class="line-card"><div class="line-card-head"><strong>Line item <span class="line-number"></span></strong><button class="remove-button" type="button" aria-label="Remove line item">Remove</button></div><div class="line-fields"><label>Date<input data-field="date" type="date" required></label><label>Vendor<input data-field="vendor" type="text" maxlength="80" placeholder="Vendor" required></label><label class="wide">Description<input data-field="description" type="text" maxlength="160" placeholder="What was purchased?" required></label><label>Category<input data-field="category" type="text" maxlength="60" placeholder="Office" required></label><label>Amount<input data-field="amount" type="number" min="0.01" max="9999999.99" step="0.01" placeholder="0.00" required></label><fieldset class="wide"><legend>Charge to</legend><div class="location-options"></div><span class="hint">Select one or more locations.</span></fieldset></div></article></template>
<script>
(() => {
  const locations = document.querySelector('#locations');
  const lines = document.querySelector('#line-items');
  const locationTemplate = document.querySelector('#location-template');
  const lineTemplate = document.querySelector('#line-template');
  let nextLine = 0;

  function locationNames() {
    return [...locations.querySelectorAll('input[name="locations"]')].map((input, i) => ({value: input.value, label: input.value.trim() || 'Location ' + (i + 1)}));
  }
  function refreshLocations() {
    const names = locationNames();
    [...lines.querySelectorAll('.line-card')].forEach(card => {
      const selected = new Set([...card.querySelectorAll('.location-options input:checked')].map(input => input.dataset.index));
      const box = card.querySelector('.location-options');
      box.innerHTML = '';
      names.forEach((loc, i) => {
        const label = document.createElement('label');
        label.className = 'check-option';
        const input = document.createElement('input');
        input.type = 'checkbox'; input.name = 'line_locations_' + card.dataset.index; input.value = String(i); input.dataset.index = String(i);
        input.checked = selected.has(String(i));
        label.append(input, document.createTextNode(loc.label)); box.append(label);
      });
    });
  }
  function renumber() { [...lines.children].forEach((card, i) => card.querySelector('.line-number').textContent = i + 1); }
  function addLocation(value = '') {
    if (locations.children.length >= 25) return;
    const row = locationTemplate.content.firstElementChild.cloneNode(true);
    row.querySelector('input').value = value;
    row.querySelector('input').addEventListener('input', refreshLocations);
    row.querySelector('.remove-button').addEventListener('click', () => { if (locations.children.length > 1) { lines.querySelectorAll('.location-options input').forEach(input => input.checked = false); row.remove(); refreshLocations(); } });
    locations.append(row); refreshLocations();
  }
  function addLine() {
    if (lines.children.length >= 100) return;
    const card = lineTemplate.content.firstElementChild.cloneNode(true);
    const index = nextLine++; card.dataset.index = index;
    card.querySelectorAll('[data-field]').forEach(input => input.name = 'line_' + index + '_' + input.dataset.field);
    card.querySelector('[data-field="date"]').valueAsDate = new Date();
    card.querySelector('.remove-button').addEventListener('click', () => { if (lines.children.length > 1) { card.remove(); renumber(); } });
    lines.append(card); refreshLocations(); renumber();
  }
  document.querySelector('#add-location').addEventListener('click', () => addLocation());
  document.querySelector('#add-line').addEventListener('click', addLine);
  document.querySelector('input[name="invoice_date"]').valueAsDate = new Date();
  document.querySelector('#invoice-form').addEventListener('submit', event => {
    for (const card of lines.querySelectorAll('.line-card')) if (!card.querySelector('.location-options input:checked')) { event.preventDefault(); alert('Choose at least one location for every line item.'); return; }
  });
  addLocation(''); addLine();
})();
</script>
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
    input[type="text"], input[type="password"], input[type="file"], input[type="date"], input[type="number"], select, textarea {
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
    .schema-strip {
      overflow: hidden;
      border: 1px solid var(--line);
      border-radius: 8px;
      background: #ffffff;
      margin: 8px 0 28px;
    }
    .schema-strip code {
      display: block;
      padding: 18px;
      background: #17201a;
      color: #f7f8f5;
      font-size: 18px;
      line-height: 1.4;
      overflow-x: auto;
    }
    .schema-grid {
      display: grid;
      grid-template-columns: repeat(6, minmax(0, 1fr));
      gap: 1px;
      background: var(--line);
    }
    .schema-grid span {
      min-height: 58px;
      display: grid;
      place-items: center;
      background: linear-gradient(135deg, #ffffff 0%, #eef4ef 100%);
      color: var(--accent);
      font-size: 13px;
      font-weight: 900;
      text-transform: uppercase;
    }
    .rules-panel {
      align-self: start;
      border: 1px solid var(--line);
      border-radius: 8px;
      background: #ffffff;
      padding: 20px;
    }
    .rules-panel p { margin-bottom: 16px; }
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
    .result {
      display: grid;
      gap: 10px;
    }
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
    .brand { color: var(--ink); }
    .brand:hover { text-decoration: none; }
    nav .active { border-bottom: 2px solid var(--accent); }
    .create-intro { padding-bottom: 18px; }
    .mode-note { display: flex; gap: 8px; align-items: baseline; margin: 0 0 24px; padding: 14px 16px; border-left: 4px solid var(--accent); background: #eef4ef; color: var(--muted); font-size: 14px; }
    .mode-note strong { color: var(--ink); white-space: nowrap; }
    .invoice-form { padding: 0; gap: 0; overflow: hidden; background: #fff; }
    .form-section { padding: 26px; border-bottom: 1px solid var(--line); }
    .form-section-heading { display: flex; align-items: flex-start; gap: 14px; margin-bottom: 20px; }
    .form-section-heading > span { flex: 0 0 auto; display: grid; place-items: center; width: 34px; height: 34px; border-radius: 50%; background: var(--accent); color: #fff; font-size: 12px; font-weight: 900; }
    .form-section-heading h3 { font-size: 20px; }
    .form-section-heading p { margin: 3px 0 0; font-size: 14px; line-height: 1.4; }
    .field-grid, .line-fields { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; }
    .form-section > label { margin-top: 16px; }
    .location-list, .line-items { display: grid; gap: 12px; margin-bottom: 14px; }
    .location-row { display: grid; grid-template-columns: 1fr auto; align-items: end; gap: 12px; }
    .secondary-button, .remove-button { min-height: 38px; border: 1px solid #b7c4bb; background: #fff; color: var(--accent); }
    .remove-button { padding: 0 12px; font-size: 13px; }
    .line-card { padding: 18px; border: 1px solid var(--line); border-radius: 7px; background: var(--panel); }
    .line-card-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 16px; }
    .line-card-head strong { font-size: 15px; }
    .line-fields .wide { grid-column: 1 / -1; }
    fieldset { min-width: 0; margin: 0; padding: 0; border: 0; }
    legend { margin-bottom: 8px; font-weight: 700; font-size: 14px; }
    .location-options { display: flex; flex-wrap: wrap; gap: 8px; }
    .check-option { display: flex; grid: none; align-items: center; gap: 7px; padding: 9px 11px; border: 1px solid #bcc7bf; border-radius: 6px; background: #fff; font-weight: 600; cursor: pointer; }
    .check-option:has(input:checked) { border-color: var(--accent); background: #e8f2ed; color: var(--accent); }
    .check-option input { accent-color: var(--accent); }
    fieldset .hint { display: block; margin-top: 7px; }
    .form-submit { display: flex; align-items: center; justify-content: space-between; gap: 18px; padding: 22px 26px; background: #eef4ef; }
    .form-submit div { display: grid; gap: 3px; }
    .form-submit span { color: var(--muted); font-size: 13px; }
    .success-result { margin-top: 20px; justify-items: start; }
    @media (max-width: 720px) {
      main { margin: 24px auto; }
      header { display: block; }
      nav { margin-top: 12px; flex-wrap: wrap; }
      h2 { font-size: 28px; }
      button { width: 100%; }
      .button-link { width: 100%; }
      .link-button { width: auto; }
      .process-visual, .schema-grid, .grid, .steps, .split { grid-template-columns: 1fr; }
      .process-visual div { min-height: 96px; }
      .cta-row, .callout { display: grid; }
      .mode-note, .form-submit { display: grid; }
      .field-grid, .line-fields { grid-template-columns: 1fr; }
      .line-fields .wide { grid-column: auto; }
      .form-section { padding: 20px 16px; }
    }
  {{end}}`))
	for _, page := range []*template.Template{publicPage, createPage} {
		if _, err := page.AddParseTree("style", style.Lookup("style").Tree); err != nil {
			panic(err)
		}
	}
}

type appConfig struct {
	Port       string
	DBPath     string
	TrustProxy bool
}

type app struct {
	cfg appConfig
	db  *sql.DB
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
	if len(os.Args) > 1 && os.Args[1] == "clear-ip-bans" {
		if err := clearIPBans(); err != nil {
			log.Fatal(err)
		}
		log.Printf("cleared Finli Live IP usage ledger")
		return
	}

	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(jobsDir, 0o755); err != nil {
		log.Fatalf("failed to create %s: %v", jobsDir, err)
	}
	if err := pruneJobs(jobsDir, time.Now().Add(-jobTTL)); err != nil {
		log.Printf("failed to prune old jobs: %v", err)
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

	application := &app{cfg: cfg, db: db}
	mux := http.NewServeMux()
	mux.HandleFunc("/", application.publicHandler)
	mux.HandleFunc("/create", application.createHandler)
	mux.HandleFunc("/invoices", application.invoicesHandler)
	mux.HandleFunc("/jobs", application.jobsHandler)
	mux.HandleFunc("/jobs/", application.downloadHandler)

	addr := "0.0.0.0:" + cfg.Port
	log.Printf("finli-live listening on http://%s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func loadConfig() (appConfig, error) {
	get := func(key string) string {
		if value, ok := os.LookupEnv(key); ok {
			return strings.TrimSpace(value)
		}
		return ""
	}

	port := get("PORT")
	if port == "" {
		port = "9876"
	}
	dbPath := get("DB_PATH")
	if dbPath == "" {
		dbPath = defaultDBPath
	}
	dbPath = filepath.Clean(dbPath)

	trustProxyValue := get("TRUST_PROXY")
	if trustProxyValue == "" {
		trustProxyValue = "false"
	}
	trustProxy, err := strconv.ParseBool(trustProxyValue)
	if err != nil {
		return appConfig{}, fmt.Errorf("TRUST_PROXY must be true or false: %w", err)
	}

	return appConfig{
		Port:       strings.TrimPrefix(port, ":"),
		DBPath:     dbPath,
		TrustProxy: trustProxy,
	}, nil
}

func initDB(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS invoice_usage (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ip TEXT NOT NULL,
  used_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_invoice_usage_ip_time
ON invoice_usage (ip, used_at);

CREATE TABLE IF NOT EXISTS form_invoice_usage (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ip TEXT NOT NULL,
  used_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_form_invoice_usage_ip_time
ON form_invoice_usage (ip, used_at);
`)
	return err
}

func clearIPBans() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		return fmt.Errorf("failed to create database directory: %w", err)
	}
	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open sqlite database: %w", err)
	}
	defer db.Close()
	if err := initDB(db); err != nil {
		return fmt.Errorf("failed to initialize sqlite database: %w", err)
	}
	_, err = db.Exec(`DELETE FROM invoice_usage; DELETE FROM form_invoice_usage`)
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

func (a *app) createHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/create" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	renderTemplate(w, http.StatusOK, createPage, viewData{})
}

func (a *app) invoicesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if ok, err := a.consumeUsage(r, "form_invoice_usage", maxFormInvoicesPerIP); err != nil {
		http.Error(w, "invoice generator unavailable", http.StatusInternalServerError)
		log.Printf("failed to rate limit form invoice: %v", err)
		return
	} else if !ok {
		renderTemplate(w, http.StatusTooManyRequests, createPage, viewData{Error: "DAILY LIMIT REACHED: this IP address has created 25 standalone invoices in the last 24 hours. Try again later."})
		return
	}

	invoice, err := parseFormInvoice(r)
	if err != nil {
		renderTemplate(w, http.StatusBadRequest, createPage, viewData{Error: err.Error()})
		return
	}
	job, err := runFormInvoice(invoice)
	if err != nil {
		log.Printf("form invoice generation failed: %v", err)
		renderTemplate(w, http.StatusInternalServerError, createPage, viewData{Error: "PDF GENERATION FAILURE: the invoice could not be created. Please try again."})
		return
	}
	renderTemplate(w, http.StatusOK, createPage, viewData{JobID: job.id, Outputs: job.outputs})
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

func (a *app) jobsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if ok, err := a.consumeGeneration(r); err != nil {
		http.Error(w, "invoice generator unavailable", http.StatusInternalServerError)
		log.Printf("failed to rate limit generation: %v", err)
		return
	} else if !ok {
		renderTemplate(w, http.StatusTooManyRequests, publicPage, viewData{Error: "DAILY LIMIT REACHED: this IP address has used its 10 invoice generations for the last 24 hours. Try again later."})
		return
	}
	if err := pruneJobs(jobsDir, time.Now().Add(-jobTTL)); err != nil {
		log.Printf("failed to prune old jobs: %v", err)
	}

	job, err := runJob(w, r)
	if err != nil {
		renderTemplate(w, http.StatusBadRequest, publicPage, viewData{Error: err.Error()})
		return
	}

	renderTemplate(w, http.StatusOK, publicPage, viewData{
		JobID:   job.id,
		Outputs: job.outputs,
	})
}

func (a *app) consumeGeneration(r *http.Request) (bool, error) {
	return a.consumeUsage(r, "invoice_usage", maxUsesPerIP)
}

func (a *app) consumeUsage(r *http.Request, table string, limit int) (bool, error) {
	if table != "invoice_usage" && table != "form_invoice_usage" {
		return false, errors.New("invalid usage ledger")
	}
	ip := a.clientIP(r)
	now := time.Now()
	cutoff := now.Add(-usageWindow).Unix()
	if _, err := a.db.Exec(`DELETE FROM `+table+` WHERE used_at < ?`, cutoff); err != nil {
		return false, err
	}

	var recent int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE ip = ? AND used_at >= ?`, ip, cutoff).Scan(&recent); err != nil {
		return false, err
	}
	if recent >= limit {
		return false, nil
	}

	_, err := a.db.Exec(`INSERT INTO `+table+` (ip, used_at) VALUES (?, ?)`, ip, now.Unix())
	return err == nil, err
}

func (a *app) downloadHandler(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "jobs" || parts[3] != "download" {
		http.NotFound(w, r)
		return
	}

	jobID := filepath.Base(parts[1])
	location := filepath.Base(parts[2])
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

type jobResult struct {
	id      string
	outputs []jobOutput
}

type locationSpec struct {
	Name        string
	Slug        string
	TitleSuffix string
}

func pruneJobs(root string, cutoff time.Time) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		createdAt := jobCreatedAt(entry)
		if createdAt.IsZero() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			createdAt = info.ModTime()
		}
		if createdAt.Before(cutoff) {
			if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func jobCreatedAt(entry os.DirEntry) time.Time {
	name := entry.Name()
	if len(name) < len("20060102T150405Z") {
		return time.Time{}
	}
	createdAt, err := time.Parse("20060102T150405Z", name[:len("20060102T150405Z")])
	if err != nil {
		return time.Time{}
	}
	return createdAt
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
	jobRoot, err := filepath.Abs(filepath.Join(jobsDir, jobID))
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
	locations, err := inferLocations(uploadDir)
	if err != nil {
		return jobResult{}, err
	}

	if err := sortReceipts(uploadDir, sortedDir, locations); err != nil {
		return jobResult{}, err
	}

	locationParts, err := generateLocationInvoices(sortedDir, workDir, invoiceName, locations)
	if err != nil {
		return jobResult{}, err
	}

	outputs := make([]jobOutput, 0, len(locations))
	for _, loc := range locations {
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
		if err := generateInvoicePDF(locationWorkDir, receiptDir, locationInvoiceName); err != nil {
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

func saveUploads(files []*multipart.FileHeader, receiptsDir string) error {
	for _, fileHeader := range files {
		rel, err := cleanUploadPath(fileHeader.Filename)
		if err != nil {
			return err
		}

		target := filepath.Join(receiptsDir, rel)
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
	base := filepath.Base(clean)
	ext := strings.ToLower(filepath.Ext(clean))
	if ext != ".pdf" {
		return "", fmt.Errorf("INVALID FILE EXTENSION: uploaded directory must contain only .pdf files: %s", name)
	}
	return base, nil
}

func mustLocationSpec(name string) locationSpec {
	loc, err := newLocationSpec(name)
	if err != nil {
		panic(err)
	}
	return loc
}

func inferLocations(sourceDir string) ([]locationSpec, error) {
	seen := make(map[string]string)
	err := filepath.WalkDir(sourceDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == sourceDir {
			return nil
		}
		if entry.IsDir() {
			return errors.New("INVALID DIR CONTENTS: the provided file path must not contain any subdirectories")
		}
		if strings.ToLower(filepath.Ext(path)) != ".pdf" {
			return errors.New("INVALID FILE EXTENSION: the dir must contain only .pdf files")
		}

		location, err := locationFromReceiptPath(sourceDir, path)
		if err != nil {
			return err
		}
		if location == "split" {
			return nil
		}
		key := locationKey(location)
		if key == "" {
			return fmt.Errorf("LOCATION INFERENCE FAILURE: location %q must include at least one letter or number", location)
		}
		if existing, ok := seen[key]; ok && existing != location {
			return fmt.Errorf("LOCATION INFERENCE FAILURE: duplicate inferred locations %q and %q", existing, location)
		}
		seen[key] = location
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("LOCATION INFERENCE FAILURE: %w", err)
	}

	names := make([]string, 0, len(seen))
	for _, name := range seen {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return locationSlug(names[i]) < locationSlug(names[j])
	})
	if len(names) == 0 {
		return nil, errors.New("LOCATION INFERENCE FAILURE: include at least one receipt with a concrete location; split-only folders do not define where to send invoices")
	}
	return parseLocationNames(names)
}

func locationFromReceiptPath(sourceDir, path string) (string, error) {
	rel, err := filepath.Rel(sourceDir, path)
	if err != nil {
		return "", fmt.Errorf("INVALID FILE NAME: failed to resolve receipt path: %w", err)
	}
	trimmedPath := filepath.ToSlash(rel)
	parts := strings.Split(trimmedPath, "-")
	if len(parts) != 6 {
		return "", fmt.Errorf("INVALID FILE NAME: PdfLineItem must consist of 6 distinct parts but you provided %d\n%s", len(parts), trimmedPath)
	}
	locationName := strings.TrimSuffix(strings.ToLower(parts[5]), ".pdf")
	return locationSlug(locationName), nil
}

func parseLocationNames(names []string) ([]locationSpec, error) {
	if len(names) == 0 {
		return nil, errors.New("LOCATION INFERENCE FAILURE: include at least one receipt with a concrete location")
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
			return nil, fmt.Errorf("LOCATION INFERENCE FAILURE: duplicate location %q", loc.Name)
		}
		seen[key] = struct{}{}
		locations = append(locations, loc)
	}
	return locations, nil
}

func newLocationSpec(name string) (locationSpec, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return locationSpec{}, errors.New("LOCATION INFERENCE FAILURE: location names cannot be blank")
	}
	slug := locationSlug(name)
	if slug == "" {
		return locationSpec{}, fmt.Errorf("LOCATION INFERENCE FAILURE: location %q must include at least one letter or number", name)
	}
	return locationSpec{
		Name:        name,
		Slug:        slug,
		TitleSuffix: strings.ToUpper(name),
	}, nil
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

type invoiceLineItem struct {
	SourceDir   string
	Path        string
	TrimmedPath string
	Date        string
	Vendor      string
	CostCents   int64
	Description string
	Category    string
	Location    string
}

type expenseCategory struct {
	Name      string
	LineItems []invoiceLineItem
	Total     int64
}

func sortReceipts(sourceDir, outDir string, locations []locationSpec) error {
	lineItems, err := lineItemsFromDir(sourceDir, locations)
	if err != nil {
		return err
	}

	for _, loc := range locations {
		if err := os.MkdirAll(filepath.Join(outDir, loc.Slug), 0o755); err != nil {
			return fmt.Errorf("FAILED TO CREATE DIR: failed to create %s: %w", loc.Slug, err)
		}
	}

	for _, item := range lineItems {
		if item.Location == "split" {
			splits, err := splitLineItem(item, locations)
			if err != nil {
				return err
			}
			for _, split := range splits {
				if err := copyLineItem(item.Path, outDir, split); err != nil {
					return err
				}
			}
			continue
		}
		if err := copyLineItem(item.Path, outDir, item); err != nil {
			return err
		}
	}
	return nil
}

func splitLineItem(item invoiceLineItem, locations []locationSpec) ([]invoiceLineItem, error) {
	if len(locations) == 0 {
		return nil, errors.New("PDF SPLIT ERROR: cannot split a receipt without locations")
	}
	base := item.CostCents / int64(len(locations))
	remainder := item.CostCents % int64(len(locations))
	splits := make([]invoiceLineItem, 0, len(locations))
	var total int64
	for i, loc := range locations {
		split := item
		split.Location = loc.Slug
		split.CostCents = base
		if int64(i) >= int64(len(locations))-remainder {
			split.CostCents++
		}
		total += split.CostCents
		splits = append(splits, split)
	}
	if total != item.CostCents {
		return nil, fmt.Errorf("PDF SPLIT ERROR: split costs do not equal original cost for %s", item.TrimmedPath)
	}
	return splits, nil
}

func copyLineItem(sourcePath, outDir string, item invoiceLineItem) error {
	destDir := filepath.Join(outDir, item.Location)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("FAILED TO CREATE DIR: failed to create %s: %w", destDir, err)
	}
	dest := filepath.Join(destDir, lineItemFileName(item))
	src, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("FILE COPY FAILURE: failed to open %s: %w", sourcePath, err)
	}
	defer src.Close()
	dst, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("FILE COPY FAILURE: failed to create %s: %w", dest, err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return fmt.Errorf("FILE COPY FAILURE: failed to copy %s to %s: %w", sourcePath, dest, err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("FILE COPY FAILURE: failed to close %s: %w", dest, err)
	}
	return nil
}

func lineItemFileName(item invoiceLineItem) string {
	return strings.Join([]string{
		item.Date,
		item.Vendor,
		formatCents(item.CostCents),
		item.Description,
		item.Category,
		item.Location + ".pdf",
	}, "-")
}

func lineItemsFromDir(sourceDir string, locations []locationSpec) ([]invoiceLineItem, error) {
	allowedLocations := make(map[string]struct{}, len(locations)+1)
	allowedLocations["split"] = struct{}{}
	for _, loc := range locations {
		allowedLocations[loc.Slug] = struct{}{}
	}

	var items []invoiceLineItem
	err := filepath.WalkDir(sourceDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == sourceDir {
			return nil
		}
		if entry.IsDir() {
			return errors.New("INVALID DIR CONTENTS: the provided file path must not contain any subdirectories")
		}
		if strings.ToLower(filepath.Ext(path)) != ".pdf" {
			return errors.New("INVALID FILE EXTENSION: the dir must contain only .pdf files")
		}
		item, err := newLineItem(sourceDir, path, allowedLocations)
		if err != nil {
			return err
		}
		items = append(items, item)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("PDF DISCOVERY FAILURE: %w", err)
	}
	return items, nil
}

func newLineItem(sourceDir, path string, allowedLocations map[string]struct{}) (invoiceLineItem, error) {
	rel, err := filepath.Rel(sourceDir, path)
	if err != nil {
		return invoiceLineItem{}, fmt.Errorf("INVALID FILE NAME: failed to resolve receipt path: %w", err)
	}
	trimmedPath := filepath.ToSlash(rel)
	parts := strings.Split(trimmedPath, "-")
	if len(parts) != 6 {
		return invoiceLineItem{}, fmt.Errorf("INVALID FILE NAME: PdfLineItem must consist of 6 distinct parts but you provided %d\n%s", len(parts), trimmedPath)
	}

	date := parts[0]
	if len(date) != 6 {
		return invoiceLineItem{}, fmt.Errorf("INVALID DATE: PdfLineItem 'date' field should only consist of 6 digits like '010125'\n%s", trimmedPath)
	}
	if _, err := strconv.Atoi(date); err != nil {
		return invoiceLineItem{}, fmt.Errorf("INVALID DATE: PdfLineItem 'date' field should be a valid number\n%s", trimmedPath)
	}

	costCents, err := parseCostCents(parts[2])
	if err != nil {
		return invoiceLineItem{}, fmt.Errorf("INVALID COST: PdfLineItem 'cost' failed to convert to a decimal dollar amount\n%s", trimmedPath)
	}

	locationName := strings.TrimSuffix(strings.ToLower(parts[5]), ".pdf")
	location := locationSlug(locationName)
	if _, ok := allowedLocations[location]; !ok {
		return invoiceLineItem{}, fmt.Errorf("INVALID LOCATION: the 'location' field must be one of the inferred locations or split\n%s", trimmedPath)
	}

	return invoiceLineItem{
		SourceDir:   sourceDir,
		Path:        path,
		TrimmedPath: trimmedPath,
		Date:        date,
		Vendor:      parts[1],
		CostCents:   costCents,
		Description: parts[3],
		Category:    parts[4],
		Location:    location,
	}, nil
}

func parseCostCents(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("blank cost")
	}
	dollars, cents, hasCents := strings.Cut(value, ".")
	if dollars == "" {
		dollars = "0"
	}
	if strings.HasPrefix(dollars, "-") {
		return 0, errors.New("negative cost")
	}
	dollarPart, err := strconv.ParseInt(dollars, 10, 64)
	if err != nil {
		return 0, err
	}
	if !hasCents {
		return dollarPart * 100, nil
	}
	if len(cents) == 0 || len(cents) > 2 {
		return 0, errors.New("invalid cents")
	}
	for len(cents) < 2 {
		cents += "0"
	}
	centPart, err := strconv.ParseInt(cents, 10, 64)
	if err != nil {
		return 0, err
	}
	return dollarPart*100 + centPart, nil
}

func formatCents(cents int64) string {
	dollars := cents / 100
	remainder := cents % 100
	if remainder == 0 {
		return strconv.FormatInt(dollars, 10)
	}
	if remainder%10 == 0 {
		return fmt.Sprintf("%d.%d", dollars, remainder/10)
	}
	return fmt.Sprintf("%d.%02d", dollars, remainder)
}

func generateInvoicePDF(workDir, receiptDir, invoiceName string) error {
	lineItems, err := lineItemsFromDir(receiptDir, []locationSpec{mustLocationSpec(filepath.Base(receiptDir))})
	if err != nil {
		return err
	}
	categories := expenseCategories(lineItems)
	total := int64(0)
	for _, category := range categories {
		total += category.Total
	}

	pdf := gofpdf.New("P", "mm", "Letter", "")
	pdf.SetTitle(invoiceName, false)
	pdf.SetMargins(10, 10, 10)
	pdf.SetAutoPageBreak(true, 10)
	pdf.AddPage()
	pdf.SetFont("Helvetica", "B", 20)
	pdf.CellFormat(0, 10, fmt.Sprintf("%s: %s", invoiceName, formatCents(total)), "", 1, "L", false, 0, "")
	pdf.Ln(12)

	for _, category := range categories {
		pdf.SetFont("Helvetica", "B", 16)
		pdf.CellFormat(0, 8, fmt.Sprintf("%s => %s", category.Name, formatCents(category.Total)), "", 1, "L", false, 0, "")
		pdf.Ln(1)
		pdf.SetFont("Helvetica", "", 12)
		for _, item := range category.LineItems {
			pdf.MultiCell(0, 6, fmt.Sprintf("[%s] [%s] [%s] [%s]", item.Date, item.Description, item.Vendor, formatCents(item.CostCents)), "", "L", false)
			pdf.Ln(1)
		}
		pdf.Ln(4)
	}

	outputPath := filepath.Join(workDir, invoiceFileName(invoiceName))
	if err := pdf.OutputFileAndClose(outputPath); err != nil {
		return fmt.Errorf("PDF RENDER FAILURE: failed to render output pdf file: %s: %w", filepath.Base(outputPath), err)
	}
	return nil
}

func expenseCategories(lineItems []invoiceLineItem) []expenseCategory {
	indexes := make(map[string]int)
	var categories []expenseCategory
	for _, item := range lineItems {
		idx, ok := indexes[item.Category]
		if !ok {
			idx = len(categories)
			indexes[item.Category] = idx
			categories = append(categories, expenseCategory{Name: item.Category})
		}
		categories[idx].LineItems = append(categories[idx].LineItems, item)
		categories[idx].Total += item.CostCents
	}
	return categories
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
	// pdfcpu's merge error does not include the input path, which makes a bad
	// receipt look like a problem with the entire directory. Validate each part
	// first so the user can identify and replace the actual offending file.
	for _, file := range files {
		if err := api.ValidateFile(file, conf); err != nil {
			return fmt.Errorf("PDF MERGE FAILURE: invalid PDF %q: %w", filepath.Base(file), err)
		}
	}
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

type formInvoice struct {
	Name      string
	Date      string
	BillTo    string
	Reference string
	Notes     string
	Locations []locationSpec
	Items     []formLineItem
}

type formLineItem struct {
	Date        string
	Vendor      string
	Description string
	Category    string
	CostCents   int64
	Locations   []int
}

func parseFormInvoice(r *http.Request) (formInvoice, error) {
	if err := r.ParseForm(); err != nil {
		return formInvoice{}, fmt.Errorf("INVALID FORM: %w", err)
	}
	clean := func(value string, max int) (string, error) {
		value = strings.TrimSpace(value)
		if len([]rune(value)) > max || strings.ContainsAny(value, "\x00\r") {
			return "", errors.New("field is too long or contains invalid characters")
		}
		return value, nil
	}
	name, err := clean(r.FormValue("invoice_name"), 100)
	if err != nil || name == "" {
		return formInvoice{}, errors.New("INVOICE DETAILS: provide a valid invoice title")
	}
	date, err := parseISODate(r.FormValue("invoice_date"))
	if err != nil {
		return formInvoice{}, fmt.Errorf("INVOICE DETAILS: %w", err)
	}
	billTo, err := clean(r.FormValue("bill_to"), 120)
	if err != nil {
		return formInvoice{}, errors.New("INVOICE DETAILS: bill-to value is invalid")
	}
	reference, err := clean(r.FormValue("reference"), 80)
	if err != nil {
		return formInvoice{}, errors.New("INVOICE DETAILS: reference is invalid")
	}
	notes, err := clean(r.FormValue("notes"), 800)
	if err != nil {
		return formInvoice{}, errors.New("INVOICE DETAILS: notes are invalid")
	}

	locationNames := r.Form["locations"]
	if len(locationNames) == 0 || len(locationNames) > maxFormLocations {
		return formInvoice{}, fmt.Errorf("LOCATIONS: provide between 1 and %d locations", maxFormLocations)
	}
	for i, value := range locationNames {
		value, cleanErr := clean(value, 60)
		if cleanErr != nil || value == "" {
			return formInvoice{}, fmt.Errorf("LOCATIONS: location %d is invalid", i+1)
		}
		locationNames[i] = value
	}
	locations, err := parseLocationNames(locationNames)
	if err != nil {
		return formInvoice{}, fmt.Errorf("LOCATIONS: %w", err)
	}

	indexSet := make(map[int]struct{})
	for key := range r.Form {
		if !strings.HasPrefix(key, "line_") || !strings.HasSuffix(key, "_date") {
			continue
		}
		middle := strings.TrimSuffix(strings.TrimPrefix(key, "line_"), "_date")
		index, err := strconv.Atoi(middle)
		if err == nil && index >= 0 {
			indexSet[index] = struct{}{}
		}
	}
	if len(indexSet) == 0 || len(indexSet) > maxFormLineItems {
		return formInvoice{}, fmt.Errorf("LINE ITEMS: provide between 1 and %d line items", maxFormLineItems)
	}
	indexes := make([]int, 0, len(indexSet))
	for index := range indexSet {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	items := make([]formLineItem, 0, len(indexes))
	for row, index := range indexes {
		prefix := "line_" + strconv.Itoa(index) + "_"
		itemDate, dateErr := parseISODate(r.FormValue(prefix + "date"))
		vendor, vendorErr := clean(r.FormValue(prefix+"vendor"), 80)
		description, descriptionErr := clean(r.FormValue(prefix+"description"), 160)
		category, categoryErr := clean(r.FormValue(prefix+"category"), 60)
		cost, costErr := parseCostCents(r.FormValue(prefix + "amount"))
		if dateErr != nil || vendorErr != nil || descriptionErr != nil || categoryErr != nil || costErr != nil || vendor == "" || description == "" || category == "" || cost <= 0 || cost > 999999999 {
			return formInvoice{}, fmt.Errorf("LINE ITEM %d: complete every field with a valid positive amount", row+1)
		}
		var selected []int
		seen := make(map[int]struct{})
		for _, raw := range r.Form["line_locations_"+strconv.Itoa(index)] {
			locIndex, convErr := strconv.Atoi(raw)
			if convErr != nil || locIndex < 0 || locIndex >= len(locations) {
				return formInvoice{}, fmt.Errorf("LINE ITEM %d: contains an invalid location", row+1)
			}
			if _, ok := seen[locIndex]; !ok {
				seen[locIndex] = struct{}{}
				selected = append(selected, locIndex)
			}
		}
		if len(selected) == 0 {
			return formInvoice{}, fmt.Errorf("LINE ITEM %d: choose at least one location", row+1)
		}
		sort.Ints(selected)
		items = append(items, formLineItem{Date: itemDate, Vendor: vendor, Description: description, Category: category, CostCents: cost, Locations: selected})
	}
	return formInvoice{Name: name, Date: date, BillTo: billTo, Reference: reference, Notes: notes, Locations: locations, Items: items}, nil
}

func parseISODate(value string) (string, error) {
	parsed, err := time.Parse("2006-01-02", strings.TrimSpace(value))
	if err != nil {
		return "", errors.New("provide a valid date")
	}
	return parsed.Format("Jan 2, 2006"), nil
}

func runFormInvoice(invoice formInvoice) (jobResult, error) {
	jobID, err := randomID()
	if err != nil {
		return jobResult{}, err
	}
	jobRoot := filepath.Join(jobsDir, jobID)
	if err := os.MkdirAll(jobRoot, 0o755); err != nil {
		return jobResult{}, err
	}
	output := filepath.Join(jobRoot, "standalone.pdf")
	if err := generateFormInvoicePDF(output, invoice); err != nil {
		_ = os.RemoveAll(jobRoot)
		return jobResult{}, err
	}
	return jobResult{id: jobID, outputs: []jobOutput{{Slug: "standalone", FileName: "finli-standalone-invoice.pdf", Path: output}}}, nil
}

func generateFormInvoicePDF(output string, invoice formInvoice) error {
	pdf := gofpdf.New("P", "mm", "Letter", "")
	pdf.SetTitle(invoice.Name, false)
	pdf.SetMargins(14, 14, 14)
	pdf.SetAutoPageBreak(true, 16)
	pdf.AddPage()
	pdf.SetTextColor(40, 105, 92)
	pdf.SetFont("Helvetica", "B", 10)
	pdf.CellFormat(0, 6, "FINLI LIVE  /  STANDALONE INVOICE", "", 1, "L", false, 0, "")
	pdf.SetTextColor(23, 32, 26)
	pdf.SetFont("Helvetica", "B", 23)
	pdf.MultiCell(0, 10, invoice.Name, "", "L", false)
	pdf.Ln(2)
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(104, 116, 109)
	pdf.CellFormat(32, 6, "Invoice date", "", 0, "L", false, 0, "")
	pdf.SetTextColor(23, 32, 26)
	pdf.CellFormat(60, 6, invoice.Date, "", 0, "L", false, 0, "")
	if invoice.Reference != "" {
		pdf.SetTextColor(104, 116, 109)
		pdf.CellFormat(25, 6, "Reference", "", 0, "L", false, 0, "")
		pdf.SetTextColor(23, 32, 26)
		pdf.CellFormat(0, 6, invoice.Reference, "", 1, "L", false, 0, "")
	} else {
		pdf.Ln(6)
	}
	if invoice.BillTo != "" {
		pdf.SetTextColor(104, 116, 109)
		pdf.CellFormat(32, 6, "Bill to", "", 0, "L", false, 0, "")
		pdf.SetTextColor(23, 32, 26)
		pdf.CellFormat(0, 6, invoice.BillTo, "", 1, "L", false, 0, "")
	}
	pdf.Ln(8)

	drawHeader := func() {
		pdf.SetFillColor(23, 32, 26)
		pdf.SetTextColor(255, 255, 255)
		pdf.SetFont("Helvetica", "B", 8)
		for _, col := range []struct {
			label string
			width float64
			align string
		}{{"DATE", 22, "L"}, {"DESCRIPTION / VENDOR", 74, "L"}, {"CATEGORY", 34, "L"}, {"LOCATION", 38, "L"}, {"AMOUNT", 20, "R"}} {
			pdf.CellFormat(col.width, 8, col.label, "", 0, col.align, true, 0, "")
		}
		pdf.Ln(8)
	}
	drawHeader()
	var total int64
	locationTotals := make([]int64, len(invoice.Locations))
	for _, item := range invoice.Items {
		if pdf.GetY() > 245 {
			pdf.AddPage()
			drawHeader()
		}
		locNames := make([]string, 0, len(item.Locations))
		base, remainder := item.CostCents/int64(len(item.Locations)), item.CostCents%int64(len(item.Locations))
		for i, locIndex := range item.Locations {
			share := base
			if int64(i) >= int64(len(item.Locations))-remainder {
				share++
			}
			locationTotals[locIndex] += share
			locNames = append(locNames, invoice.Locations[locIndex].Name)
		}
		pdf.SetTextColor(23, 32, 26)
		pdf.SetFont("Helvetica", "", 8)
		pdf.CellFormat(22, 12, item.Date, "B", 0, "L", false, 0, "")
		detail := item.Description + " / " + item.Vendor
		if len([]rune(detail)) > 43 {
			detail = string([]rune(detail)[:40]) + "..."
		}
		pdf.CellFormat(74, 12, detail, "B", 0, "L", false, 0, "")
		pdf.CellFormat(34, 12, item.Category, "B", 0, "L", false, 0, "")
		locationsText := strings.Join(locNames, ", ")
		if len([]rune(locationsText)) > 21 {
			locationsText = string([]rune(locationsText)[:18]) + "..."
		}
		pdf.CellFormat(38, 12, locationsText, "B", 0, "L", false, 0, "")
		pdf.CellFormat(20, 12, "$"+formatMoney(item.CostCents), "B", 1, "R", false, 0, "")
		total += item.CostCents
	}
	pdf.Ln(5)
	pdf.SetFont("Helvetica", "B", 13)
	pdf.SetTextColor(23, 32, 26)
	pdf.CellFormat(168, 9, "TOTAL", "", 0, "R", false, 0, "")
	pdf.CellFormat(20, 9, "$"+formatMoney(total), "", 1, "R", false, 0, "")
	pdf.Ln(8)
	pdf.SetFont("Helvetica", "B", 11)
	pdf.CellFormat(0, 7, "Allocation by location", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	for i, loc := range invoice.Locations {
		pdf.SetTextColor(104, 116, 109)
		pdf.CellFormat(90, 6, loc.Name, "", 0, "L", false, 0, "")
		pdf.SetTextColor(23, 32, 26)
		pdf.CellFormat(30, 6, "$"+formatMoney(locationTotals[i]), "", 1, "R", false, 0, "")
	}
	if invoice.Notes != "" {
		pdf.Ln(7)
		pdf.SetFont("Helvetica", "B", 10)
		pdf.CellFormat(0, 6, "Notes", "", 1, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 9)
		pdf.SetTextColor(104, 116, 109)
		pdf.MultiCell(0, 5, invoice.Notes, "", "L", false)
	}
	if err := pdf.OutputFileAndClose(output); err != nil {
		return fmt.Errorf("PDF RENDER FAILURE: %w", err)
	}
	return nil
}

func formatMoney(cents int64) string { return fmt.Sprintf("%d.%02d", cents/100, cents%100) }
