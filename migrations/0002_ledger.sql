-- Ledger: append-only double-entry journal with invariants enforced in SQL.
-- Money is BIGINT minor units. Balances are maintained by triggers; entries are immutable.
--
-- Semantics (TigerBeetle-style two-phase transfers):
--   posted  transaction: entries move accounts.balance immediately.
--   pending transaction: entries reserve funds in pending_debits / pending_credits only;
--                        available = balance - pending_debits. Resolving pending->posted moves
--                        the reserved amounts into balance; pending->voided releases them.

create table ledger.accounts (
  id              uuid primary key,
  code            text not null unique,
  name            text not null,
  currency        char(3) not null,
  kind            text not null check (kind in ('asset','liability','equity','income','expense')),
  owner_id        uuid null,
  allow_negative  boolean not null default false,
  balance         bigint not null default 0,
  pending_debits  bigint not null default 0 check (pending_debits >= 0),
  pending_credits bigint not null default 0 check (pending_credits >= 0),
  version         bigint not null default 0,
  created_at      timestamptz not null default now()
);
create index accounts_owner_idx on ledger.accounts (owner_id);

create table ledger.transactions (
  id              uuid primary key,
  idempotency_key text not null unique,
  payload_hash    bytea not null,
  description     text not null,
  status          text not null check (status in ('pending','posted','voided')),
  effective_date  date not null,
  created_at      timestamptz not null default now(),
  resolved_at     timestamptz null,
  expires_at      timestamptz null,
  metadata        jsonb not null default '{}'::jsonb
);
create index transactions_pending_expiry_idx on ledger.transactions (expires_at) where status = 'pending';

create table ledger.entries (
  id             uuid primary key,
  transaction_id uuid not null references ledger.transactions (id),
  account_id     uuid not null references ledger.accounts (id),
  amount         bigint not null check (amount <> 0),
  currency       char(3) not null,
  balance_after  bigint not null default 0,
  seq            bigserial not null,
  created_at     timestamptz not null default now()
);
create index entries_account_seq_idx on ledger.entries (account_id, seq desc);
create index entries_transaction_idx on ledger.entries (transaction_id);

create table ledger.period_closes (
  currency       char(3) primary key,
  closed_through date not null,
  closed_at      timestamptz not null default now()
);

create view ledger.available_balances as
  select id as account_id, currency, balance, pending_debits, pending_credits,
         balance - pending_debits as available
  from ledger.accounts;

-- ---------------------------------------------------------------------------
-- Entry insert: validate currency, period close, apply to account, stamp balance_after.
create function ledger.entries_before_insert() returns trigger language plpgsql as $$
declare
  acct ledger.accounts%rowtype;
  txn  ledger.transactions%rowtype;
  closed date;
begin
  select * into txn from ledger.transactions where id = new.transaction_id for update;
  if not found then
    raise exception 'ledger_transaction_missing: %', new.transaction_id;
  end if;
  if txn.status = 'voided' then
    raise exception 'ledger_immutable: cannot add entries to a voided transaction';
  end if;
  -- Entries belong to the database transaction that created the ledger
  -- transaction (created_at is forced to now() on insert): once committed,
  -- a posted or pending transaction never takes more entries, balanced or not.
  if txn.created_at <> now() then
    raise exception 'ledger_immutable: transaction % is committed; entries can only be added when it is created', txn.id;
  end if;
  if txn.status = 'pending' and txn.resolved_at is not null then
    raise exception 'ledger_immutable: cannot add entries to a resolved transaction';
  end if;

  select * into acct from ledger.accounts where id = new.account_id for update;
  if not found then
    raise exception 'ledger_account_missing: %', new.account_id;
  end if;
  if acct.currency <> new.currency then
    raise exception 'ledger_currency_mismatch: account % is % but entry is %', acct.code, acct.currency, new.currency;
  end if;

  select closed_through into closed from ledger.period_closes where currency = new.currency;
  if closed is not null and txn.effective_date <= closed then
    raise exception 'ledger_period_closed: % is closed through %', new.currency, closed;
  end if;

  if txn.status = 'posted' then
    acct.balance := acct.balance + new.amount;
  elsif new.amount < 0 then
    acct.pending_debits := acct.pending_debits - new.amount;
  else
    acct.pending_credits := acct.pending_credits + new.amount;
  end if;

  if not acct.allow_negative and acct.balance - acct.pending_debits < 0 then
    raise exception 'ledger_insufficient_funds: account % available would be %', acct.code, acct.balance - acct.pending_debits;
  end if;

  update ledger.accounts
     set balance = acct.balance, pending_debits = acct.pending_debits,
         pending_credits = acct.pending_credits, version = version + 1
   where id = acct.id;
  new.balance_after := acct.balance - acct.pending_debits;
  return new;
end $$;
create trigger entries_before_insert before insert on ledger.entries
  for each row execute function ledger.entries_before_insert();

-- Entries are immutable.
create function ledger.raise_immutable() returns trigger language plpgsql as $$
begin
  raise exception 'ledger_immutable: % rows cannot be % ', tg_table_name, lower(tg_op);
end $$;
create trigger entries_immutable before update or delete on ledger.entries
  for each row execute function ledger.raise_immutable();

-- Every transaction balances to zero per currency at commit time.
create function ledger.entries_balanced() returns trigger language plpgsql as $$
declare bad record;
begin
  select e.transaction_id, e.currency, sum(e.amount) as total
    into bad
    from ledger.entries e
   where e.transaction_id = new.transaction_id
   group by e.transaction_id, e.currency
  having sum(e.amount) <> 0
   limit 1;
  if found then
    raise exception 'ledger_unbalanced: transaction % sums to % %', bad.transaction_id, bad.total, bad.currency;
  end if;
  return null;
end $$;
create constraint trigger entries_balanced after insert on ledger.entries
  deferrable initially deferred for each row execute function ledger.entries_balanced();

-- A transaction must have entries (at least two) when it commits.
create function ledger.transaction_has_entries() returns trigger language plpgsql as $$
declare n int;
begin
  select count(*) into n from ledger.entries where transaction_id = new.id;
  if n < 2 then
    raise exception 'ledger_unbalanced: transaction % has % entries', new.id, n;
  end if;
  return null;
end $$;
create constraint trigger transaction_has_entries after insert on ledger.transactions
  deferrable initially deferred for each row execute function ledger.transaction_has_entries();

-- Transactions are stamped by the database: created_at is the inserting
-- transaction's start (entries_before_insert relies on it), and a posted
-- transaction is resolved at creation.
create function ledger.transactions_before_insert() returns trigger language plpgsql as $$
begin
  new.created_at := now();
  if new.status = 'posted' then
    new.resolved_at := new.created_at;
  else
    new.resolved_at := null;
  end if;
  if new.status = 'voided' then
    raise exception 'ledger_immutable: a transaction cannot be created voided';
  end if;
  return new;
end $$;
create trigger transactions_before_insert before insert on ledger.transactions
  for each row execute function ledger.transactions_before_insert();

-- Transactions: only pending -> posted|voided, exactly once; everything else immutable.
create function ledger.transactions_before_update() returns trigger language plpgsql as $$
declare
  e record;
  closed date;
begin
  if old.status <> 'pending' then
    raise exception 'ledger_immutable: transaction % is %', old.id, old.status;
  end if;
  if new.status not in ('posted','voided') then
    raise exception 'ledger_immutable: pending transaction may only become posted or voided';
  end if;
  if new.id <> old.id or new.idempotency_key <> old.idempotency_key or new.payload_hash <> old.payload_hash
     or new.description <> old.description or new.effective_date <> old.effective_date
     or new.created_at <> old.created_at or new.metadata <> old.metadata
     or new.expires_at is distinct from old.expires_at then
    raise exception 'ledger_immutable: only status may change when resolving';
  end if;
  -- Posting a hold moves balances on its effective date, so the period
  -- close applies; voiding moves no posted balance and stays allowed.
  if new.status = 'posted' then
    for e in select distinct currency from ledger.entries where transaction_id = old.id loop
      select closed_through into closed from ledger.period_closes where currency = e.currency;
      if closed is not null and old.effective_date <= closed then
        raise exception 'ledger_period_closed: % is closed through %', e.currency, closed;
      end if;
    end loop;
  end if;
  new.resolved_at := now();

  for e in select account_id, amount from ledger.entries where transaction_id = old.id loop
    if new.status = 'posted' then
      if e.amount < 0 then
        update ledger.accounts set balance = balance + e.amount, pending_debits = pending_debits + e.amount,
               version = version + 1 where id = e.account_id;
      else
        update ledger.accounts set balance = balance + e.amount, pending_credits = pending_credits - e.amount,
               version = version + 1 where id = e.account_id;
      end if;
    else
      if e.amount < 0 then
        update ledger.accounts set pending_debits = pending_debits + e.amount, version = version + 1 where id = e.account_id;
      else
        update ledger.accounts set pending_credits = pending_credits - e.amount, version = version + 1 where id = e.account_id;
      end if;
    end if;
  end loop;
  return new;
end $$;
create trigger transactions_before_update before update on ledger.transactions
  for each row execute function ledger.transactions_before_update();
create trigger transactions_no_delete before delete on ledger.transactions
  for each row execute function ledger.raise_immutable();

-- Accounts: only name and owner_id may be changed directly; balance,
-- pending and version columns only by the ledger triggers above.
-- allow_negative is fixed at creation (flipping it would let an account
-- overdraw without any entry trail).
create function ledger.accounts_guard() returns trigger language plpgsql as $$
begin
  if new.id <> old.id or new.code <> old.code or new.currency <> old.currency or new.kind <> old.kind
     or new.allow_negative <> old.allow_negative or new.created_at <> old.created_at then
    raise exception 'ledger_immutable: account identity, code, currency, kind and allow_negative cannot change';
  end if;
  if (new.balance <> old.balance or new.pending_debits <> old.pending_debits or new.pending_credits <> old.pending_credits
      or new.version <> old.version)
     and pg_trigger_depth() <= 1 then
    raise exception 'ledger_immutable: balances change only through entries';
  end if;
  return new;
end $$;
create trigger accounts_guard before update on ledger.accounts
  for each row execute function ledger.accounts_guard();
create trigger accounts_no_delete before delete on ledger.accounts
  for each row execute function ledger.raise_immutable();
