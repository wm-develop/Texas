UPDATE rooms
SET max_players = 10
WHERE status = 'OPEN' AND max_players <> 10;
