# Inventory & Sales Tracker

A local Go and MySQL portfolio app for item master data (SKU, name, brand, category and unit), purchase requests and approvals, purchase orders, goods receipts (GR), goods issues (GI), sales, moving-average cost of goods sold (COGS), stock valuation, monthly closing and change history. The interface uses plain HTML, CSS and JavaScript served by Go, keeping setup lightweight and avoiding a separate frontend build for this small application. New purchasing, receipt, issue and sales documents have a header and multiple detail lines. Their line postings update the existing stock ledger atomically. Previous single-item transactions remain available in the earlier-transactions sections.

## Run on this computer

MySQL was installed separately at `D:\Andree\Portfolio Projects\Local MySQL`. The database is `inventory_portfolio`, listening only on `127.0.0.1:3307`. With Go installed, start the database and application from this folder:

```powershell
.\start-local.ps1
```

Then open http://127.0.0.1:8080. MySQL data remains in the `Local MySQL\data` folder. The start script uses the demo-only connection settings in the code. These are local development credentials; change them for any real environment.

## Run elsewhere

Install Go 1.22 or newer and MySQL 8.4. One option is `docker compose up -d`, which starts a local MySQL database on port 3307 with development credentials. Then run `go run .`. The app creates its tables and history triggers on first start; the schema is defined in `migrate()` in `main.go`. To load the optional `sample-data.sql`, first start the app once, then execute that script against a fresh demo database with the current month open. Do not run it repeatedly or against real data. The MySQL user must have CREATE, ALTER, INDEX, TRIGGER, SELECT, INSERT and UPDATE privileges. The supplied Docker setup disables binary logging so the application user can create history triggers. For an existing installation, supply a properly configured database and set `MYSQL_DSN` with `parseTime=true&multiStatements=true`; do not change server-wide settings without understanding their impact.

## Inventory and period workflow

1. Create a brand and category, then add an item.
2. Create and approve a purchase request; create a purchase order from it.
3. Receive the order in one or more goods receipts. A request line can be ordered once and its order quantity must match the request; partial receipts are supported. Receipt cost changes the item's moving average cost.
4. Record goods issues or sales. Sales save the unit cost at posting time and report COGS as quantity × unit cost.
5. The ongoing month is the only open stock-posting period. Transactions use the database server's current date and the app does not accept backdated dates. When the calendar moves to the next month, stock posting is blocked until the previous month is closed. Run the open-month recalculation if needed, then close the ended month from Reports. The close saves an item-by-item stock quantity, unit COGS and value, and opens the next month. If several months elapsed, close them one at a time.
6. Recalculation replays the open month's movements from the preceding closed snapshot. It validates stock and sale links and rolls back on discrepancies. It never changes closed-month sales costs or closing snapshots. Closed periods cannot be reopened through the app. Monthly COGS here means moving-average unit cost; actual COGS expense is shown in sales and sales reports.

MySQL triggers save before/after snapshots for master data, requests, orders, movements, sales, recalculations, monthly balances and period changes in `transaction_history` within the same database transaction. Existing records present when history is first enabled are labeled `BASELINE`; earlier changes cannot be reconstructed. The history module searches the complete history and loads older records in pages of 100. The sample chair, transactions and sale in the local database were created to verify the workflow.

## Boundaries

This is a simplified local demonstration, not an accounting system: there are no user accounts, role-based approvals, journal entries, multiple warehouses, taxes, returns, foreign currencies, period reopening or production-grade decimal arithmetic. Amounts are stored to four decimal places; calculation currently uses floating-point arithmetic. It does not implement Odoo or SAP in full. Do not expose it to the internet or rely on it for financial reporting without security, accuracy and accounting review.

## Design references

Period closing and valuation were informed by the inventory valuation and stock closing concepts in [Odoo documentation](https://www.odoo.com/documentation/19.0/applications/finance/accounting/get_started/inventory_valuation.html), and the effects of prior-period goods movements described in [SAP documentation](https://help.sap.com/docs/SAP_S4HANA_CLOUD/af9ef57f504840d2b81be8667206d485/475fb6531de6b64ce10000000a174cb4.html). This demo chooses a stricter simplified rule: it does not permit prior-period posting or reopening through the app.

## Testing

`go test ./...` skips the integration test unless `INVENTORY_TEST_DSN` is set. To run the full test, create a new, empty disposable MySQL database with the same permissions as the application account, set `INVENTORY_TEST_DSN` to its DSN (including `parseTime=true&multiStatements=true`), and run `go test -v ./...`. The test intentionally changes its database, including simulated past-period records; never point it at the application database. It covers request approval, partial receipts, multi-line documents, atomic rollback, insufficient stock, moving-average COGS, item editing, monthly close, history, and closed-period protection.
