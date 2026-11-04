-- Control plane: principals, roles, account holders, maker-checker approvals
-- and the policy decision log.
--
-- Separation of duties is enforced in the schema as well as in Go:
--   * static  (roles_static_sod): nobody holds both payments.initiator and
--     payments.approver;
--   * dynamic (decisions_dynamic_sod): nobody decides an approval they
--     initiated, and only pending approvals take decisions.
-- The approval lifecycle is a state machine (approvals_state_machine):
-- pending -> approved|rejected|expired, approved -> executed; the request
-- columns never change and decisions are append-only.
-- Trigger errors are SQLSTATE P0001 with messages prefixed sod_conflict,
-- sod_self_approval, approval_not_pending or approval_immutable.
--
-- control.principals / control.roles are also declared (identically, with
-- "if not exists") by 0006_auth.sql, so either migration may land first.
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

create function control.roles_static_sod() returns trigger language plpgsql as $$
declare
  opposite text;
begin
  opposite := case new.role
    when 'payments.approver'  then 'payments.initiator'
    when 'payments.initiator' then 'payments.approver'
  end;
  if opposite is null then
    return new;
  end if;
  -- Serialise role changes per principal so two concurrent grants cannot
  -- both miss each other (READ COMMITTED callers included).
  perform 1 from control.principals where id = new.principal_id for no key update;
  if exists (select 1 from control.roles
             where principal_id = new.principal_id and role = opposite) then
    raise exception 'sod_conflict: principal % cannot hold both % and %',
      new.principal_id, new.role, opposite;
  end if;
  return new;
end;
$$;

create trigger roles_static_sod
  before insert or update on control.roles
  for each row execute function control.roles_static_sod();

-- Who may act on a ledger account as its customer (joint accounts have >1).
create table control.account_holders (
  account_id   uuid not null references ledger.accounts(id),
  principal_id uuid not null references control.principals(id),
  primary key (account_id, principal_id)
);
create index account_holders_principal_idx on control.account_holders (principal_id);

-- A maker-checker request. subject holds the full request (for transfers,
-- the TransferInput incl. idempotency_key), which is unique per approval.
create table control.approvals (
  id                    uuid primary key,
  kind                  text not null,
  subject               jsonb not null,
  amount                bigint not null check (amount > 0),
  currency              char(3) not null,
  initiator_id          uuid not null references control.principals(id),
  required_approvals    int not null check (required_approvals >= 1),
  -- joint-account rule: one approve decision must come from a holder of
  -- the from-account other than the initiator.
  requires_holder_approval boolean not null default false,
  not_before            timestamptz null,
  status                text not null default 'pending'
                        check (status in ('pending','approved','rejected','executed','expired')),
  created_at            timestamptz not null default now(),
  ledger_transaction_id uuid null references ledger.transactions(id),
  check ((status = 'executed') = (ledger_transaction_id is not null))
);
create unique index approvals_idempotency_key_idx on control.approvals ((subject->>'idempotency_key'));
create index approvals_initiator_idx on control.approvals (initiator_id, created_at desc);
create index approvals_pending_idx on control.approvals (created_at) where status = 'pending';

create function control.approvals_state_machine() returns trigger language plpgsql as $$
begin
  if tg_op = 'DELETE' then
    raise exception 'approval_immutable: approval % cannot be deleted', old.id;
  end if;
  if new.id <> old.id or new.kind <> old.kind or new.subject <> old.subject
     or new.amount <> old.amount or new.currency <> old.currency
     or new.initiator_id <> old.initiator_id or new.required_approvals <> old.required_approvals
     or new.requires_holder_approval <> old.requires_holder_approval
     or new.created_at <> old.created_at then
    raise exception 'approval_immutable: approval % request columns cannot change', old.id;
  end if;
  if old.status in ('executed','rejected','expired') then
    raise exception 'approval_immutable: approval % is % (terminal)', old.id, old.status;
  end if;
  if new.status <> old.status and not (
       (old.status = 'pending'  and new.status in ('approved','rejected','expired'))
    or (old.status = 'approved' and new.status = 'executed')) then
    raise exception 'approval_immutable: approval % cannot go from % to %', old.id, old.status, new.status;
  end if;
  return new;
end;
$$;

create trigger approvals_state_machine
  before update or delete on control.approvals
  for each row execute function control.approvals_state_machine();

create table control.approval_decisions (
  approval_id uuid not null references control.approvals(id),
  approver_id uuid not null references control.principals(id),
  decision    text not null check (decision in ('approve','reject')),
  reason      text not null default '',
  at          timestamptz not null default now(),
  primary key (approval_id, approver_id)
);

create function control.decisions_dynamic_sod() returns trigger language plpgsql as $$
declare
  a record;
begin
  -- for share: a concurrent status change waits for this decision (and
  -- vice versa), so the pending check cannot go stale before commit.
  select initiator_id, status into a from control.approvals where id = new.approval_id for share;
  if not found then
    return new; -- the foreign key reports the missing approval
  end if;
  if new.approver_id = a.initiator_id then
    raise exception 'sod_self_approval: principal % initiated approval % and cannot decide it',
      new.approver_id, new.approval_id;
  end if;
  if a.status <> 'pending' then
    raise exception 'approval_not_pending: approval % is %', new.approval_id, a.status;
  end if;
  return new;
end;
$$;

create trigger decisions_dynamic_sod
  before insert or update on control.approval_decisions
  for each row execute function control.decisions_dynamic_sod();

-- Decisions are append-only evidence.
create function control.decisions_append_only() returns trigger language plpgsql as $$
begin
  raise exception 'approval_immutable: decisions on approval % cannot be %d',
    old.approval_id, lower(tg_op);
end;
$$;

create trigger decisions_append_only
  before update or delete on control.approval_decisions
  for each row execute function control.decisions_append_only();

-- One row per policy evaluation or decision attempt, including refusals.
create table control.decision_log (
  id           bigserial primary key,
  at           timestamptz not null default now(),
  principal_id uuid null,
  action       text not null,
  input        jsonb not null default '{}',
  outcome      text not null
               check (outcome in ('posted','approval_created','approved','rejected','executed','denied')),
  reason       text not null default ''
);
create index decision_log_principal_idx on control.decision_log (principal_id, at);
