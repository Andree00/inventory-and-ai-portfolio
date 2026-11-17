# Session recap

- This is a local Windows setup of the Inventory & Sales Tracker and Ollama Document Summarizer. The apps use localhost ports 8080 and 3000; MySQL uses port 3307. The local launcher and portable tools live outside this repository in the surrounding Codex workspace.
- The inventory database contains sample master data and linked purchasing, stock and sales transactions. Sample prices are in IDR. The database and its backups are local and are not included in Git.
- The inventory app now has customer and vendor masters, searchable selection fields, IDR formatting, a fixed sidebar and independently scrollable lists. Existing transaction party names are retained as historical snapshots.
- The summarizer uses local Ollama models (`llama3.2:3b` and `all-minilm`) without an API key. This PC uses CPU inference because its GPU driver was incompatible with Ollama's CUDA runtime.
- Inventory integration tests use a separate disposable MySQL database via `INVENTORY_TEST_DSN`. Never run them against the application database. The interface changes were not visually verified because browser access to localhost was declined.
- The inventory README and `start-local.ps1` describe an older machine-specific MySQL location. This session's launcher is in the Codex workspace's `outputs` folder; do not assume that older path exists.
