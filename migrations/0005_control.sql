-- Control plane: principals, roles, account holders, maker-checker approvals
-- and the policy decision log.
--
-- Separation of duties is enforced in the schema as well as in Go:
--   * static  (roles_static_sod): nobody holds both payments.initiator and
--     payments.approver, and customers hold no payments.* role;
--   * dynamic (decisions_dynamic_sod): nobody decides an approval they
--     initiated, only holders of the from-account or payments.approver
--     holders decide, and only pending, unexpired approvals take decisions.
-- The approval lifecycle is a state machine (approvals_state_machine):
-- pending -> approved|rejected|expired, approved -> executed; the request
-- columns never change and decisions are append-only. Becoming approved
-- needs the quorum of approve decisions (and, for joint accounts, one from
-- a co-holder); becoming executed needs the approval's own ledger posting.
-- Trigger errors are SQLSTATE P0001 with messages prefixed sod_conflict,
-- sod_role_not_allowed, sod_self_approval, sod_not_eligible,
-- approval_not_pending, approval_quorum, approval_holder_required,
-- approval_ledger_mismatch or approval_immutable.
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
  pkind    text;
begin
  if new.role like 'payments.%' then
    -- Serialise role changes per principal so two concurrent grants cannot
    -- both miss each other (READ COMMITTED callers included).
    select kind into pkind from control.principals where id = new.principal_id for no key update;
    if pkind = 'customer' then
      raise exception 'sod_role_not_allowed: % cannot be granted to customer principal %',
        new.role, new.principal_id;
    end if;
  end if;
  opposite := case new.role
    when 'payments.approver'  then 'payments.initiator'
    when 'payments.initiator' then 'payments.approver'
  end;
  if opposite is null then
    return new;
  end if;
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
  -- A pending approval not decided by then expires (swept hourly by serve).
  -- Equals created_at + 7 days: both defaults are the inserting
  -- transaction's now().
  expires_at            timestamptz not null default (now() + interval '7 days'),
  ledger_transaction_id uuid null references ledger.transactions(id),
  check ((status = 'executed') = (ledger_transaction_id is not null))
);
create unique index approvals_idempotency_key_idx on control.approvals ((subject->>'idempotency_key'));
create index approvals_initiator_idx on control.approvals (initiator_id, created_at desc);
create index approvals_pending_idx on control.approvals (created_at) where status = 'pending';
create index approvals_expiry_idx on control.approvals (expires_at) where status = 'pending';

create function control.approvals_state_machine() returns trigger language plpgsql as $$
declare
  approvals_count int;
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
  -- The deadline can be brought forward while pending, never extended.
  if new.expires_at > old.expires_at then
    raise exception 'approval_immutable: approval % expiry cannot be extended', old.id;
  end if;

  if new.status = 'approved' and old.status = 'pending' then
    if old.expires_at <= now() then
      raise exception 'approval_not_pending: approval % expired at %', old.id, old.expires_at;
    end if;
    select count(*) into approvals_count from control.approval_decisions
     where approval_id = old.id and decision = 'approve';
    if approvals_count < old.required_approvals then
      raise exception 'approval_quorum: approval % has % of % approvals', old.id, approvals_count, old.required_approvals;
    end if;
    if old.requires_holder_approval and not exists (
         select 1 from control.approval_decisions d
           join control.account_holders h
             on h.principal_id = d.approver_id and h.account_id = (old.subject->>'from_account_id')::uuid
          where d.approval_id = old.id and d.decision = 'approve' and d.approver_id <> old.initiator_id) then
      raise exception 'approval_holder_required: approval % needs an approval by another holder of the from-account', old.id;
    end if;
  end if;

  if new.status = 'executed' and old.status = 'approved' then
    -- The posting must be this approval's own transfer: its idempotency
    -- key, posted, debiting from and crediting to by the amount.
    if not exists (
         select 1 from ledger.transactions t
          where t.id = new.ledger_transaction_id
            and t.idempotency_key = old.subject->>'idempotency_key'
            and t.status = 'posted'
            and exists (select 1 from ledger.entries e where e.transaction_id = t.id
                         and e.account_id = (old.subject->>'from_account_id')::uuid and e.amount = -old.amount)
            and exists (select 1 from ledger.entries e where e.transaction_id = t.id
                         and e.account_id = (old.subject->>'to_account_id')::uuid and e.amount = old.amount)) then
      raise exception 'approval_ledger_mismatch: ledger transaction % is not the posting of approval %',
        new.ledger_transaction_id, old.id;
    end if;
  elsif new.ledger_transaction_id is distinct from old.ledger_transaction_id then
    raise exception 'approval_immutable: approval % ledger transaction is set only on execution', old.id;
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
  select initiator_id, status, expires_at, subject into a from control.approvals where id = new.approval_id for share;
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
  if a.expires_at <= now() then
    raise exception 'approval_not_pending: approval % expired at %', new.approval_id, a.expires_at;
  end if;
  -- Only a holder of the from-account or a payments.approver may decide.
  if not exists (select 1 from control.account_holders
                  where account_id = (a.subject->>'from_account_id')::uuid and principal_id = new.approver_id)
     and not exists (select 1 from control.roles
                      where principal_id = new.approver_id and role = 'payments.approver') then
    raise exception 'sod_not_eligible: principal % is neither a holder of the from-account nor a payments.approver',
      new.approver_id;
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
               check (outcome in ('posted','approval_created','approved','rejected','executed','expired','denied')),
  reason       text not null default ''
);
create index decision_log_principal_idx on control.decision_log (principal_id, at);
