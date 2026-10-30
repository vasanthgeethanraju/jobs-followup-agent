# jobs-followup-agent

An AI agent in **Go** that reads my job applications in Gmail and helps me follow up with companies that haven't replied.

> 🚧 Work in progress. I'm building it step by step to learn LLMs, agents, RAG, and MCP. See the roadmap below.

## What it does today

1. Finds the applications I emailed (Gmail label `!Jobs`, sent by me) on the day they turn 7 days old.
2. Skips anything that's already settled: a reply in the thread, a `Rejected` or `Next Steps` label (in that thread or any other thread with the same company), a bounce, or an earlier follow-up.
3. Uses an LLM (Google Gemini, structured JSON output) to read the original email and pull out the company, role, and contact name, then write a short follow-up.
4. Checks the text against style rules (plain words, no em dashes, no buzzwords) and asks the model again if it breaks one.
5. Saves each follow-up as a **Gmail draft** in the same thread. It never sends anything. I review and send.
6. Remembers everything in Postgres, so it never drafts twice and catches up on days it didn't run.

## Architecture

```
cmd/agent ──► internal/mailbox   Gmail API: OAuth login, search, threads, drafts (with retry/backoff)
          ├─► internal/followup  the follow-up rules (pure logic, unit-tested)
          ├─► internal/writer    prompt + style guardrails + email assembly (tested with a fake LLM)
          ├─► internal/llm       LLM interface; Gemini client over plain HTTP; Fake for tests
          └─► internal/store     Postgres (pgx): applications + runs
```

**Design choices**
- **LLM behind an interface**, so the provider (Gemini, Claude, a local model) is a one-line swap and tests run with no network.
- **The LLM only writes what needs judgment.** The greeting, opener, and sign-off are plain code, so they always come out the same.
- **Drafts, not sends.** A human approves every email.
- **Postgres + pgvector** so the RAG step can store embeddings in the same database.

## Run it locally

```bash
cp .env.example .env            # add your Gemini API key
docker compose up -d            # Postgres (+ pgvector) on port 5433
# put your Google OAuth "Desktop app" credentials.json in the project root
go run ./cmd/agent              # preview the follow-ups in the terminal
go run ./cmd/agent -draft       # create Gmail drafts
go test ./...
```

## Roadmap

- [x] Gmail OAuth, and finding applications that need a follow-up
- [x] Postgres memory (no duplicates, catch-up on missed days)
- [x] LLM: structured output, style guardrails, Gmail drafts
- [ ] RAG: match my projects and experience to each company for more personal follow-ups
- [ ] Agent loop: the LLM chooses which tools to call
- [ ] MCP server: ask "who should I follow up with?" from Claude Desktop
- [ ] Deploy on AWS (Lambda + EventBridge schedule)

Built with Go, Gmail API, Google Gemini, Postgres/pgvector, and Docker.
