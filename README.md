# Finli Live

Finli Live is a public invoice generator built around a filename schema:

```text
date-vendor-cost-description-category-location.pdf
```

Example:

```text
010125-target-10.95-pants-uniforms-southroads.pdf
```

That name is the data model. Finli Live reads each receipt filename, parses the date, vendor, cost, description, category, and location, infers the location set from the uploaded folder, then sorts receipts, splits shared expenses, calculates invoice totals, generates invoice cover pages, and merges final location-specific PDF packets.

## Public Workflow

Open the homepage, choose the invoice name, upload one flat folder of receipt PDFs, and generate the finished PDFs. The app infers locations from receipt filenames, so there is no `locations.json` setup step and no login portal.

Each IP address can run 10 invoice generations per rolling 24-hour window. Usage is stored in `data/main.sqlite`, and entries older than 24 hours are pruned before each rate-limit check so the ledger does not grow indefinitely. Generated job directories under `data/jobs` are also removed after 24 hours.

## Standalone Form Invoices

Open `/create` when you want to make an invoice without uploading receipts. The form supports:

- invoice details, billing information, a reference, and notes
- up to 100 line items in one invoice
- up to 25 named locations
- assigning each line item to one or several locations
- exact even splits, including deterministic allocation of remainder cents
- a single downloadable PDF with line-item, grand-total, and per-location summaries

The standalone builder has its own limit of 25 generated invoices per IP address in a rolling 24-hour window. It does not consume the folder generator's 10-generation allowance. Both usage ledgers and generated PDFs are pruned on the same 24-hour schedule.

## Naming Schema

Every receipt PDF must use exactly six dash-separated parts:

```text
MMDDYY-vendor-cost-description-category-location.pdf
```

- `MMDDYY`: six digits, such as `010125`
- `vendor`: where the purchase happened
- `cost`: dollar amount, such as `10.95`
- `description`: short purchase description
- `category`: expense category used for invoice totals
- `location`: the invoice destination, or `split`

The special `split` location divides the receipt cost across every concrete location inferred from the folder. Include at least one non-split receipt for each location that should receive an invoice packet.

## Locations

Locations are inferred from receipt filenames. For example, this folder generates Southroads and Utica invoice packets and splits the paper purchase across both:

```text
010125-target-10.95-pants-uniforms-southroads.pdf
010225-amazon-12.00-paper-office-split.pdf
010325-walmart-8.50-snacks-meals-utica.pdf
```

A folder containing only `split` receipts is rejected because Finli Live cannot infer where those shared expenses should go.

## Requirements

- Go

## Install

```bash
make install
```

## Run

```bash
make run
```

Open `http://localhost:9876` for the public generator.

To use a different dedicated port:

```bash
PORT=9877 make run
```

## Clear Rate Limits

After installing the binary, clear the public IP usage ledger with:

```bash
finli-live clear-ip-bans
```
