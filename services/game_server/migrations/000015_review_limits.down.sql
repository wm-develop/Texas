DROP INDEX IF EXISTS hand_reviews_user_status_idx;
ALTER TABLE hand_reviews DROP COLUMN IF EXISTS next_attempt_at, DROP COLUMN IF EXISTS attempts;
ALTER TABLE review_access DROP COLUMN IF EXISTS max_in_flight, DROP COLUMN IF EXISTS daily_limit;
ALTER TABLE review_settings DROP COLUMN IF EXISTS max_in_flight_per_user;
