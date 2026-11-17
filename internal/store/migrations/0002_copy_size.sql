-- How much a mirror copy takes up where it lives, measured after each good copy.
ALTER TABLE copies ADD COLUMN last_size INTEGER NOT NULL DEFAULT 0;
