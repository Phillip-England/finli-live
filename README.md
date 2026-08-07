# Finli Live

Finli Live wraps the `finli` CLI in a local web app. It uploads a receipt directory, runs `finli sort`, generates location-specific invoices with `finli generate`, then merges each invoice with its sorted receipt PDFs into separate location PDFs.

## Locations

The admin area includes a `Locations JSON` page for creating a `locations.json` file. Put one location per line, download the file, then include it at the top level of the receipt directory upload.

During invoice generation, use the Location target field to choose outputs:

- `split` generates one PDF for every location in `locations.json`.
- `split-Southroads-Utica` generates PDFs only for Southroads and Utica. Keep appending `-LocationName` to include more locations.

If no `locations.json` file is uploaded, the app falls back to the original Southroads and Utica locations.

## Requirements

- Go
- `finli` installed and available on `PATH`

If `finli` is missing, the app exits at startup and points you to `github.com/phillip-england/finli`.

## Install

```bash
make install
```

## Run

Create `config/.env` locally:

```env
ADMIN_USERNAME=admin
ADMIN_PASSWORD=change-me-now
SESSION_SECRET=replace-with-a-long-random-secret-before-deployment
DB_PATH=data/main.sqlite
PORT=9876
TRUST_PROXY=false
```

```bash
make run
```

Open `http://localhost:9876` for the public overview. The invoice generator is protected at `http://localhost:9876/admin`.

To use a different dedicated port:

```bash
PORT=9877 make run
```
