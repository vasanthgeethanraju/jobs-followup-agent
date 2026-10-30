-- The database "shape". Runs every time the agent starts;
-- "IF NOT EXISTS" makes it safe to run again and again (idempotent).

-- One row per application conversation (Gmail thread).
CREATE TABLE IF NOT EXISTS applications (
    thread_id    TEXT PRIMARY KEY,         -- Gmail's conversation ID: unique, so it's our key
    company_key  TEXT NOT NULL,            -- "acme.com" (or full address for gmail.com etc.)
    to_address   TEXT NOT NULL,
    subject      TEXT NOT NULL,
    applied_at   TIMESTAMPTZ NOT NULL,     -- TIMESTAMPTZ = timestamp with time zone (always use this)
    status       TEXT NOT NULL,            -- follow_up_due | skipped | drafted  (later: followed_up, replied)
    reason       TEXT NOT NULL,            -- why: "no reply yet", "they replied", ...
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    checked_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- An index makes "WHERE status = ..." fast without scanning every row.
CREATE INDEX IF NOT EXISTS applications_status_idx ON applications (status);

-- One row per agent run. Gives the agent memory of WHEN it last ran,
-- so a skipped day gets caught up next time.
CREATE TABLE IF NOT EXISTS runs (
    id           BIGSERIAL PRIMARY KEY,    -- auto-incrementing number
    started_at   TIMESTAMPTZ NOT NULL,
    finished_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    checked      INT NOT NULL,             -- how many applications we looked at
    due          INT NOT NULL              -- how many need a follow-up
);

-- Step 5 additions. "ADD COLUMN IF NOT EXISTS" upgrades an existing table safely.
-- (This is a hand-made "migration": a versioned change to the database shape.
-- When we have more of these, a tool like goose will track them for us.)
ALTER TABLE applications ADD COLUMN IF NOT EXISTS company    TEXT;        -- "Pond", read by the LLM
ALTER TABLE applications ADD COLUMN IF NOT EXISTS role       TEXT;        -- "Software Engineer"
ALTER TABLE applications ADD COLUMN IF NOT EXISTS draft_id   TEXT;        -- Gmail draft we created
ALTER TABLE applications ADD COLUMN IF NOT EXISTS drafted_at TIMESTAMPTZ;
