-- Audit: append-only, hash-chained event log with Ed25519-signed daily roots.
-- hash = sha256(prev_hash || canonical_json({at, actor_id, action, subject, payload})).
-- Genesis prev_hash is 32 zero bytes. Chain growth is serialised by row-locking control.audit_head (select ... for update).

create table control.audit_events (
  seq        bigserial primary key,
  at         timestamptz not null default now(),
  actor_id   uuid null,
  action     text not null,
  subject    text not null,
  payload    jsonb not null default '{}',
  prev_hash  bytea not null,
  hash       bytea not null
);
create index audit_events_at_idx on control.audit_events (at);

create function control.audit_events_immutable() returns trigger language plpgsql as $$
begin
  raise exception 'audit_immutable: audit events are append-only';
end;
$$;

create trigger audit_events_immutable
  before update or delete on control.audit_events
  for each row execute function control.audit_events_immutable();

create table control.audit_roots (
  day        date primary key,
  head_seq   bigint not null,
  head_hash  bytea not null,
  signature  bytea not null,
  signed_at  timestamptz not null default now()
);

-- Single-row chain head. Append locks it with FOR UPDATE; under SERIALIZABLE a
-- concurrent committed update makes the lock raise 40001, which db.WithTx
-- retries, so the chain can never fork.
create table control.audit_head (
  id   int primary key check (id = 1),
  seq  bigint not null,
  hash bytea not null
);
insert into control.audit_head (id, seq, hash) values (1, 0, decode(repeat('00', 32), 'hex'));
