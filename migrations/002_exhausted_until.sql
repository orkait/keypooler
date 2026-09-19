-- A consumer reports a key the provider refused for the rest of its billing period; the key
-- is skipped until this time and serves again on its own once it passes.
ALTER TABLE keys ADD COLUMN exhausted_until TIMESTAMP;
