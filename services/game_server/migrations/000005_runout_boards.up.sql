ALTER TABLE hands
    ADD COLUMN runout_boards jsonb NOT NULL DEFAULT '[]'::jsonb;
