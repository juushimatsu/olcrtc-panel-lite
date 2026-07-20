ALTER TABLE instances ADD COLUMN client_id TEXT NOT NULL DEFAULT '';

UPDATE instances
SET client_id = lower(
    hex(randomblob(4)) || '-' ||
    hex(randomblob(2)) || '-' ||
    '4' || substr(hex(randomblob(2)), 2) || '-' ||
    substr('89ab', 1 + (random() & 3), 1) || substr(hex(randomblob(2)), 2) || '-' ||
    hex(randomblob(6))
)
WHERE client_id = '';

CREATE UNIQUE INDEX IF NOT EXISTS instances_client_id_idx ON instances(client_id);

ALTER TABLE subscription_entries DROP COLUMN exclave_compatible;
