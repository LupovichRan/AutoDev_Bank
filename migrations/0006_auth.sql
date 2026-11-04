-- Passkey (WebAuthn) credentials, opaque sessions with step-up, and
-- short-lived ceremony challenges.
--
-- control.principals / control.roles are owned by 0005_control.sql; they are
-- repeated here with "if not exists" and identical columns so the auth
-- package is self-sufficient whichever migration lands first.
create table if not exists control.principals (
  id           uuid primary key,
  kind         text not null check (kind in ('customer','staff','service')),
  display_name text not null,
  handle       text not null unique,
  created_at   timestamptz not null default now()
);

create table if not exists control.roles (
  principal_id uuid not null references control.principals(id),
  role         text not null,
  primary key (principal_id, role)
);

-- One row per registered authenticator (passkey).
create table control.credentials (
  id               bytea primary key,                 -- credential id (rawId)
  principal_id     uuid not null references control.principals(id),
  public_key       bytea not null,                    -- COSE-encoded public key
  attestation_type text not null default '',
  transport        text[] not null default '{}',
  flags            jsonb not null,                    -- UP/UV/BE/BS + raw byte
  authenticator    jsonb not null,                    -- AAGUID, sign count, clone warning, attachment
  created_at       timestamptz not null default now(),
  last_used_at     timestamptz
);
create index credentials_principal_idx on control.credentials (principal_id);

-- Opaque 256-bit session ids (cookie bank_session).
create table control.sessions (
  id                  text primary key,
  principal_id        uuid not null references control.principals(id),
  created_at          timestamptz not null default now(),
  expires_at          timestamptz not null,
  user_agent          text not null default '',
  stepped_up_at       timestamptz null,
  stepup_payload_hash bytea null,
  check ((stepped_up_at is null) = (stepup_payload_hash is null))
);
create index sessions_principal_idx on control.sessions (principal_id);
create index sessions_expires_idx on control.sessions (expires_at);

-- WebAuthn ceremony state between /begin and /finish; single use, 5 minutes.
create table control.webauthn_challenges (
  id           text primary key,
  kind         text not null check (kind in ('register','login','stepup')),
  principal_id uuid null references control.principals(id),
  session_data jsonb not null,                       -- webauthn.SessionData
  payload_hash bytea null,                           -- step-up only: sha256 of the payment payload
  expires_at   timestamptz not null,
  check ((kind = 'stepup') = (payload_hash is not null)),
  check (kind = 'login' or principal_id is not null)
);
create index webauthn_challenges_expires_idx on control.webauthn_challenges (expires_at);
