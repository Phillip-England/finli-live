# Finli Live

Finli Live wraps the `finli` CLI in a local web app. It uploads a receipt directory, runs `finli sort`, generates separate Utica and Southroads invoices with `finli generate`, then merges each invoice with its sorted receipt PDFs into separate Southroads and Utica PDFs.

## Requirements

- Go
- `finli` installed and available on `PATH`

If `finli` is missing, the app exits at startup and points you to `github.com/phillip-england/finli`.

## Install

```bash
make install
```

## Run

```bash
make run
```

Open `http://localhost:9876`.

To use a different dedicated port:

```bash
PORT=9877 make run
```
