create table if not exists users (
    id text primary key,
    email text not null unique,
    name text not null default '',
    password_hash text not null,
    created_at timestamptz not null default now()
);

create table if not exists events (
    id text primary key,
    title text not null,
    starts_at timestamptz not null,
    price_cents integer not null check (price_cents >= 0),
    capacity integer not null check (capacity > 0),
    available integer not null check (available >= 0),
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    check (available <= capacity)
);

create index if not exists events_starts_at_idx on events (starts_at);

create table if not exists reservations (
    id text primary key,
    order_id text not null unique,
    event_id text not null references events(id),
    user_id text not null references users(id),
    quantity integer not null check (quantity > 0),
    status text not null check (status in ('reserved', 'released')),
    created_at timestamptz not null default now()
);

create index if not exists reservations_event_id_idx on reservations (event_id);
create index if not exists reservations_user_id_idx on reservations (user_id);

create table if not exists orders (
    id text primary key,
    user_id text not null references users(id),
    event_id text not null references events(id),
    quantity integer not null check (quantity > 0),
    status text not null check (status in ('pending', 'confirmed', 'rejected')),
    reason text,
    created_at timestamptz not null default now(),
    confirmed_at timestamptz,
    updated_at timestamptz not null default now()
);

create index if not exists orders_user_id_created_at_idx on orders (user_id, created_at desc);
create index if not exists orders_event_id_idx on orders (event_id);

create table if not exists notifications (
    id text primary key,
    user_id text not null references users(id),
    order_id text not null references orders(id),
    kind text not null,
    message text not null,
    created_at timestamptz not null default now(),
    unique (order_id, kind)
);

create index if not exists notifications_user_id_created_at_idx on notifications (user_id, created_at desc);
