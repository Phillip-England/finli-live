# Finli Live

Finli Live is a local web app for turning receipt PDFs into location-specific invoice packets. It uploads a receipt directory, sorts and splits receipts by location, generates location-specific invoice pages, then merges each invoice with its sorted receipt PDFs into separate location PDFs.

## Locations

The admin area includes a `Locations JSON` page for creating a `locations.json` file. Put one location per line, download the file, then include it at the top level of the receipt directory upload.

During invoice generation, use the Location target field to choose outputs:

- `split` generates one PDF for every location in `locations.json`.
- `split-Southroads-Utica` generates PDFs only for Southroads and Utica. Keep appending `-LocationName` to include more locations.

If no `locations.json` file is uploaded, the app falls back to the original Southroads and Utica locations.

## Requirements

- Go

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
