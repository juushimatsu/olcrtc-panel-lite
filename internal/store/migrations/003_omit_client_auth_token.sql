ALTER TABLE instances ADD COLUMN omit_client_auth_token INTEGER NOT NULL DEFAULT 0;
ALTER TABLE subscriptions ADD COLUMN omit_client_auth_token INTEGER NOT NULL DEFAULT 0;
