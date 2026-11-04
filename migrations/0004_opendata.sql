-- Cached public open data: the last good snapshot per (provider, kind), and
-- a log of every refresh attempt. A failed refresh only appends to the log;
-- it never touches the snapshot, so readers keep the previous good data.
create table opendata.snapshots (
  provider   text        not null,
  kind       text        not null,
  fetched_at timestamptz not null,
  source_url text        not null,
  license    text        not null default '',
  payload    jsonb       not null check (jsonb_typeof(payload) = 'array'),
  primary key (provider, kind)
);

create table opendata.refresh_log (
  id          bigserial   primary key,
  provider    text        not null,
  kind        text        not null,
  at          timestamptz not null default now(),
  ok          bool        not null,
  error       text        not null default '',
  duration_ms int         not null check (duration_ms >= 0),
  records     int         not null default 0
);

create index refresh_log_provider_kind_idx on opendata.refresh_log (provider, kind, id desc);
