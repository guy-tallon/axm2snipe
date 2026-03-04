# CLAUDE.md

## Project Overview

axm2snipe syncs Apple Business Manager (ABM) devices into Snipe-IT asset management. Written in Go.

## Build & Run

```bash
go build -o axm2snipe .
./axm2snipe --dry-run -v     # safe test
./axm2snipe -v               # real sync
./axm2snipe --serial SERIAL  # single device
```

## Project Structure

- `main.go` — CLI entry point, flag parsing, client initialization
- `config/config.go` — YAML config loading, validation, env var overrides
- `abmclient/client.go` — Apple Business Manager API client (OAuth2, pagination, device/AppleCare endpoints)
- `snipe/client.go` — Snipe-IT API client (models, users, suppliers, assets; dry-run write protection)
- `sync/sync.go` — Core sync engine (model/supplier resolution, field mapping, create/update logic)
- `settings.example.yaml` — Example config with all options documented
- `create_fields.sh` — Helper script to create Snipe-IT custom fields via API

## Key Design Decisions

- **No hardcoded custom fields**: All Snipe-IT custom field mappings are in `settings.yaml` under `sync.field_mapping`. The left side is the Snipe-IT DB column name (`_snipeit_*_N`), the right side is the ABM source value name.
- **Dry-run is enforced at HTTP level**: When `DryRun` is true on the Snipe-IT client, `do()` blocks all non-GET requests and returns `ErrDryRun`.
- **Update-only mode**: When `update_only: true`, assets not found in Snipe-IT are skipped. No new assets, models, or suppliers are created.
- **Colors and statuses are title-cased**: ABM returns uppercase values (SILVER, ACTIVE, Paid_up_front). `titleCase()` converts underscores to spaces and title-cases (Silver, Active, Paid Up Front).
- **CDW order numbers are cleaned**: "CDW/1CJ6QLW/002" → "1CJ6QLW" via `cleanOrderNumber()`.
- **MAC addresses are auto-formatted**: Raw hex from ABM (e.g. "2CCA164BD29D") is converted to colon-separated format ("2C:CA:16:4B:D2:9D") via `formatMAC()`.
- **Models indexed by name AND number**: `loadModels()` indexes Snipe-IT models by both `Name` and `ModelNumber` for flexible matching.
- **Suppliers auto-created**: ABM's `PurchaseSourceType` is matched against existing Snipe-IT suppliers (case-insensitive).
- **Snipe-IT validation errors detected**: Snipe-IT returns HTTP 200 with `{"status":"error"}` for validation failures. The `do()` method checks for this and returns an error.
- **warranty_months calculated from purchase date**: `warranty_months = purchase_date → applecare_end` so Snipe-IT's auto-calculated "Warranty Expires" matches the actual coverage end date.

## Testing

No test files yet. Use `--dry-run -v` to verify behavior without making changes.

## Gotchas

- ABM `deviceModel` returns marketing names like "Mac mini (2024)", NOT hardware identifiers like "Mac16,10" that Jamf uses.
- Snipe-IT returns HTTP 200 with `{"status":"error","messages":{...}}` for validation failures — not HTTP 4xx.
- Snipe-IT radio/listbox fields reject values not in their predefined options — the entire update fails silently.
- Snipe-IT MAC format fields require colon-separated MACs (e.g. "2C:CA:16:4B:D2:9D").
- Snipe-IT custom field DB column names include an auto-incremented ID suffix (e.g. `_snipeit_color_7`). These are instance-specific.
- ABM `fields[orgDevices]` is a JSON:API sparse fieldset — it filters attributes to only the listed fields, not adds to them.
- The `warranty_months` field is auto-calculated and is the only non-configurable field mapping.
