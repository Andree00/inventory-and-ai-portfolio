# AI Document Summarizer — Ollama

Upload PDF or text documents, ask questions or request summaries, and inspect the supporting passages. The app chunks text, generates local embeddings, retrieves relevant passages and asks a local model to answer with citations.

## Run

Requires Node.js 22.13+ and Ollama. In PowerShell:

```powershell
ollama pull llama3.2:3b
ollama pull all-minilm
npm install
npm start
```

Open http://localhost:3000. No API key is required. Start Ollama first. Models are downloaded once and inference runs locally. The model is not guaranteed to cite correctly; check its answers against the displayed passages. PDF files must contain selectable text; scanned documents need OCR. Changing embedding models requires re-uploading documents.

This is a local demonstration without authentication or user isolation; do not expose it publicly. `npm test` tests the retrieval flow with a mocked model provider.
