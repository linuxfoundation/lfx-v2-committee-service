-- Copyright The Linux Foundation and each contributor to LFX.
-- SPDX-License-Identifier: MIT

-- +goose Up
-- +goose StatementBegin

-- Tables for the committee-service PostgreSQL store. Each table mirrors one NATS KV bucket
-- (see docs/nats-storage.md); the constraints and indexes that replace the KV lookup keys are
-- in the next migration.
--
-- Choices made for a safe backfill from KV:
--   * ids are TEXT and there are no CHECK constraints on enum-like columns, because KV does not
--     validate either and the backfill must not fail on existing data;
--   * there are no foreign keys, because KV has no referential integrity and the backfill may
--     find orphans (a later migration adds them NOT VALID once the planned `verify` command,
--     lfx-self-serve-ops#1198, reports none);
--   * dates the Go models keep as strings stay TEXT.

-- One revision per row is the optimistic-concurrency token the API exposes today (revision /
-- etag / if_match). While both stores are written the composite store sets it explicitly from the
-- KV revision; the default below applies when Postgres is the authority. At the flip the sequence
-- must be advanced above the highest KV revision so a stale pre-flip token can never match.
CREATE SEQUENCE committee_revision_seq AS BIGINT;

-- committees <- KV bucket `committees` (model.CommitteeBase)
CREATE TABLE committees (
    uid                 TEXT        PRIMARY KEY,
    project_uid         TEXT        NOT NULL,
    project_name        TEXT        NOT NULL DEFAULT '',
    project_slug        TEXT        NOT NULL DEFAULT '',
    name                TEXT        NOT NULL,
    category            TEXT        NOT NULL DEFAULT '',
    description         TEXT        NOT NULL DEFAULT '',
    website             TEXT,
    mailing_list        TEXT,
    chat_channel        TEXT,
    enable_voting       BOOLEAN     NOT NULL DEFAULT false,
    sso_group_enabled   BOOLEAN     NOT NULL DEFAULT false,
    sso_group_name      TEXT        NOT NULL DEFAULT '',
    requires_review     BOOLEAN     NOT NULL DEFAULT false,
    public              BOOLEAN     NOT NULL DEFAULT false,
    join_mode           TEXT        NOT NULL DEFAULT '',
    calendar_public     BOOLEAN     NOT NULL DEFAULT false,
    display_name        TEXT        NOT NULL DEFAULT '',
    parent_uid          TEXT,
    repository          TEXT,
    scope               JSONB       NOT NULL DEFAULT '[]',  -- []string
    deliverables        JSONB       NOT NULL DEFAULT '[]',  -- []string
    key_dates           JSONB       NOT NULL DEFAULT '[]',  -- []{date,label}
    external_sources    JSONB       NOT NULL DEFAULT '[]',  -- []ExternalSource
    charter             JSONB,                              -- *Charter {url,version,updated_at,updated_by}
    total_members       INTEGER     NOT NULL DEFAULT 0,     -- maintained by the member-event consumer
    total_voting_repos  INTEGER     NOT NULL DEFAULT 0,
    has_mailing_list    BOOLEAN     NOT NULL DEFAULT false,
    revision            BIGINT      NOT NULL DEFAULT nextval('committee_revision_seq'),
    created_at          TIMESTAMPTZ NOT NULL,
    updated_at          TIMESTAMPTZ NOT NULL
);

-- committee_settings <- KV bucket `committee-settings` (model.CommitteeSettings), 1:1 with committees.
-- Its own table because it has its own revision, is auditor-only, and holds a sensitive webhook URL
-- that must stay out of any warehouse export. writers and auditors stay on this row as JSONB.
CREATE TABLE committee_settings (
    committee_uid           TEXT        PRIMARY KEY,
    business_email_required BOOLEAN     NOT NULL DEFAULT false,
    show_meeting_attendees  BOOLEAN     NOT NULL DEFAULT false,
    member_visibility       TEXT        NOT NULL DEFAULT '',
    chat_webhook_url        TEXT,                                -- SENSITIVE
    has_chat_webhook        BOOLEAN     NOT NULL DEFAULT false,
    last_reviewed_at        TEXT,                                -- a string in the model
    last_reviewed_by        TEXT,
    writers                 JSONB       NOT NULL DEFAULT '[]',   -- []CommitteeUser
    auditors                JSONB       NOT NULL DEFAULT '[]',   -- []CommitteeUser
    revision                BIGINT      NOT NULL DEFAULT nextval('committee_revision_seq'),
    created_at              TIMESTAMPTZ NOT NULL,
    updated_at              TIMESTAMPTZ NOT NULL
);

-- committee_members <- KV bucket `committee-members` (model.CommitteeMemberBase)
CREATE TABLE committee_members (
    uid                  TEXT        PRIMARY KEY,
    committee_uid        TEXT        NOT NULL,
    username             TEXT        NOT NULL DEFAULT '',
    avatar               TEXT        NOT NULL DEFAULT '',
    email                TEXT        NOT NULL,                  -- PII
    first_name           TEXT        NOT NULL DEFAULT '',       -- PII
    last_name            TEXT        NOT NULL DEFAULT '',       -- PII
    job_title            TEXT        NOT NULL DEFAULT '',
    linkedin_profile     TEXT        NOT NULL DEFAULT '',
    role_name            TEXT        NOT NULL DEFAULT '',
    role_start_date      TEXT        NOT NULL DEFAULT '',       -- date strings in the model
    role_end_date        TEXT        NOT NULL DEFAULT '',
    appointed_by         TEXT        NOT NULL DEFAULT '',
    status               TEXT        NOT NULL DEFAULT '',
    voting_status        TEXT        NOT NULL DEFAULT '',
    voting_start_date    TEXT        NOT NULL DEFAULT '',
    voting_end_date      TEXT        NOT NULL DEFAULT '',
    organization_id      TEXT        NOT NULL DEFAULT '',       -- 18-character SFID
    organization_name    TEXT        NOT NULL DEFAULT '',
    organization_website TEXT        NOT NULL DEFAULT '',
    -- Denormalized from the committee today (CommitteeMember.NeedsSyncWith); kept for parity.
    committee_name       TEXT        NOT NULL DEFAULT '',
    committee_category   TEXT        NOT NULL DEFAULT '',
    project_uid          TEXT        NOT NULL DEFAULT '',
    project_slug         TEXT        NOT NULL DEFAULT '',
    revision             BIGINT      NOT NULL DEFAULT nextval('committee_revision_seq'),
    created_at           TIMESTAMPTZ NOT NULL,
    updated_at           TIMESTAMPTZ NOT NULL
);

-- committee_invites <- KV bucket `committee-invites` (model.CommitteeInvite)
CREATE TABLE committee_invites (
    uid                   TEXT        PRIMARY KEY,
    committee_uid         TEXT        NOT NULL,
    committee_name        TEXT        NOT NULL DEFAULT '',
    organization_required BOOLEAN     NOT NULL DEFAULT false,
    invitee_email         TEXT        NOT NULL,                 -- PII
    role                  TEXT        NOT NULL DEFAULT '',
    organization          JSONB,                                -- *CommitteeMemberOrganization {id,name,website}
    status                TEXT        NOT NULL DEFAULT '',
    inviter               JSONB,                                -- *CommitteeUser (PII)
    expires_at            TIMESTAMPTZ,                          -- NULL when the model has no expiry
    revision              BIGINT      NOT NULL DEFAULT nextval('committee_revision_seq'),
    created_at            TIMESTAMPTZ NOT NULL,
    -- New: the model only has created_at today. The backfill sets it from created_at.
    updated_at            TIMESTAMPTZ NOT NULL
);

-- committee_applications <- KV bucket `committee-applications` (model.CommitteeApplication)
CREATE TABLE committee_applications (
    uid                TEXT        PRIMARY KEY,
    committee_uid      TEXT        NOT NULL,
    applicant_email    TEXT        NOT NULL,                    -- PII
    applicant_username TEXT        NOT NULL DEFAULT '',
    message            TEXT        NOT NULL DEFAULT '',
    status             TEXT        NOT NULL DEFAULT '',
    reviewer_notes     TEXT        NOT NULL DEFAULT '',
    organization       JSONB,                                   -- *CommitteeMemberOrganization
    revision           BIGINT      NOT NULL DEFAULT nextval('committee_revision_seq'),
    created_at         TIMESTAMPTZ NOT NULL,
    -- New: the model only has created_at today. The backfill sets it from created_at.
    updated_at         TIMESTAMPTZ NOT NULL
);

-- committee_link_folders <- KV bucket `committee-folders` (model.CommitteeLinkFolder)
CREATE TABLE committee_link_folders (
    uid           TEXT        PRIMARY KEY,
    committee_uid TEXT        NOT NULL,
    name          TEXT        NOT NULL,
    created_by    JSONB,                                        -- *CommitteeUser
    updated_by    JSONB,
    revision      BIGINT      NOT NULL DEFAULT nextval('committee_revision_seq'),
    created_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL
);

-- committee_links <- KV bucket `committee-links` (model.CommitteeLink)
CREATE TABLE committee_links (
    uid           TEXT        PRIMARY KEY,
    committee_uid TEXT        NOT NULL,
    folder_uid    TEXT,
    name          TEXT        NOT NULL,
    url           TEXT        NOT NULL,
    description   TEXT        NOT NULL DEFAULT '',
    created_by    JSONB,
    updated_by    JSONB,
    revision      BIGINT      NOT NULL DEFAULT nextval('committee_revision_seq'),
    created_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL
);

-- committee_documents <- KV bucket `committee-documents-metadata` (model.CommitteeDocument).
-- The file bytes stay in the NATS Object Store, keyed by uid.
CREATE TABLE committee_documents (
    uid           TEXT        PRIMARY KEY,
    committee_uid TEXT        NOT NULL,
    folder_uid    TEXT,
    name          TEXT        NOT NULL,
    description   TEXT        NOT NULL DEFAULT '',
    file_name     TEXT        NOT NULL,
    file_size     BIGINT      NOT NULL DEFAULT 0,
    content_type  TEXT        NOT NULL DEFAULT '',
    created_by    JSONB,
    updated_by    JSONB,
    revision      BIGINT      NOT NULL DEFAULT nextval('committee_revision_seq'),
    created_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL
);

-- group_weekly_briefs <- KV buckets `group-weekly-briefs` and `group-weekly-brief-uid-index`
-- (model.GroupWeeklyBrief). The uid index bucket is replaced by the primary key.
CREATE TABLE group_weekly_briefs (
    uid                    TEXT        PRIMARY KEY,
    committee_uid          TEXT        NOT NULL,
    window_start           TIMESTAMPTZ NOT NULL,
    window_end             TIMESTAMPTZ NOT NULL,
    state                  TEXT        NOT NULL,
    error_reason           TEXT        NOT NULL DEFAULT '',
    brief_text             TEXT        NOT NULL DEFAULT '',
    source_refs            JSONB       NOT NULL DEFAULT '[]',   -- []SourceRef
    prompt_version         TEXT        NOT NULL DEFAULT '',
    model                  TEXT        NOT NULL DEFAULT '',
    regeneration_count     INTEGER     NOT NULL DEFAULT 0,
    private_source_present BOOLEAN     NOT NULL DEFAULT false,
    last_edited_at         TIMESTAMPTZ,
    last_edited_by         TEXT        NOT NULL DEFAULT '',
    revision               BIGINT      NOT NULL DEFAULT nextval('committee_revision_seq'),
    created_at             TIMESTAMPTZ NOT NULL,
    updated_at             TIMESTAMPTZ NOT NULL
);

-- group_weekly_brief_throttle <- KV bucket `group-weekly-brief-throttle`
-- (model.GroupWeeklyBriefThrottle)
CREATE TABLE group_weekly_brief_throttle (
    committee_uid      TEXT        NOT NULL,
    window_start       TIMESTAMPTZ NOT NULL,
    window_end         TIMESTAMPTZ NOT NULL,
    generates_used     INTEGER     NOT NULL DEFAULT 0,
    regenerations_used INTEGER     NOT NULL DEFAULT 0,
    window_resets_at   TIMESTAMPTZ NOT NULL,
    revision           BIGINT      NOT NULL DEFAULT nextval('committee_revision_seq'),
    PRIMARY KEY (committee_uid, window_start)
);

-- Data classification, readable by tooling (for example a warehouse export allowlist) from
-- col_description(): PII columns hold personal data, SENSITIVE is a credential. Anything that exports
-- these tables must exclude SENSITIVE columns and treat PII columns according to the data policy.
COMMENT ON COLUMN committee_settings.chat_webhook_url IS 'SENSITIVE: bearer credential (chat webhook URL), never export or index';
COMMENT ON COLUMN committees.charter IS 'PII'; -- Charter.updated_by is a user (name, email, username, avatar)
COMMENT ON COLUMN committee_members.username IS 'PII';
COMMENT ON COLUMN committee_members.avatar IS 'PII';
COMMENT ON COLUMN committee_members.email IS 'PII';
COMMENT ON COLUMN committee_members.first_name IS 'PII';
COMMENT ON COLUMN committee_members.last_name IS 'PII';
COMMENT ON COLUMN committee_members.job_title IS 'PII';
COMMENT ON COLUMN committee_members.linkedin_profile IS 'PII';
COMMENT ON COLUMN committee_settings.writers IS 'PII';
COMMENT ON COLUMN committee_settings.auditors IS 'PII';
COMMENT ON COLUMN committee_settings.last_reviewed_by IS 'PII';
COMMENT ON COLUMN committee_invites.invitee_email IS 'PII';
COMMENT ON COLUMN committee_invites.inviter IS 'PII';
COMMENT ON COLUMN committee_applications.applicant_email IS 'PII';
COMMENT ON COLUMN committee_applications.applicant_username IS 'PII';
COMMENT ON COLUMN committee_applications.message IS 'PII';
COMMENT ON COLUMN committee_applications.reviewer_notes IS 'PII';
COMMENT ON COLUMN committee_link_folders.created_by IS 'PII';
COMMENT ON COLUMN committee_link_folders.updated_by IS 'PII';
COMMENT ON COLUMN committee_links.created_by IS 'PII';
COMMENT ON COLUMN committee_links.updated_by IS 'PII';
COMMENT ON COLUMN committee_documents.created_by IS 'PII';
COMMENT ON COLUMN committee_documents.updated_by IS 'PII';
COMMENT ON COLUMN group_weekly_briefs.last_edited_by IS 'PII';
COMMENT ON COLUMN group_weekly_briefs.source_refs IS 'PII'; -- member roster excerpts name members
COMMENT ON COLUMN group_weekly_briefs.brief_text IS 'PII'; -- the narrative can repeat those names

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS group_weekly_brief_throttle;
DROP TABLE IF EXISTS group_weekly_briefs;
DROP TABLE IF EXISTS committee_documents;
DROP TABLE IF EXISTS committee_links;
DROP TABLE IF EXISTS committee_link_folders;
DROP TABLE IF EXISTS committee_applications;
DROP TABLE IF EXISTS committee_invites;
DROP TABLE IF EXISTS committee_members;
DROP TABLE IF EXISTS committee_settings;
DROP TABLE IF EXISTS committees;
DROP SEQUENCE IF EXISTS committee_revision_seq;
-- +goose StatementEnd
