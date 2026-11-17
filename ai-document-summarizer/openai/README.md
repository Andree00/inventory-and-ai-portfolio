# AI Document Summarizer — OpenAI

Upload PDF or text documents, ask questions or request summaries, and inspect supporting passages. The app chunks text, generates embeddings and retrieves relevant passages for cited answers.

## Run

Requires Node.js 22.13+, an OpenAI API key and available API credits. API usage can incur charges. Documents and questions are sent to OpenAI.

```powershell
npm install
Copy-Item .env.example .env
# Edit .env and set OPENAI_API_KEY
npm start
```

Open http://localhost:3000. The indicator confirms a key is configured, not that billing or model access works. Do not commit `.env` or share your key. PDFs must contain selectable text; scanned documents need OCR. Check model-generated answers and citations against the passages. Changing embedding models requires re-uploading documents.

This is a local demonstration without authentication or user isolation; do not expose it publicly. `npm test` tests the retrieval flow with a mocked provider. Live API usage has not been verified in this version.
