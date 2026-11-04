-- Three schemas: ledger (money), control (customers, approvals, audit, auth), opendata (cached public data).
create schema if not exists ledger;
create schema if not exists control;
create schema if not exists opendata;
