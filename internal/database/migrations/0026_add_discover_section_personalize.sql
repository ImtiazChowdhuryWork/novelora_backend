ALTER TABLE discover_sections ADD COLUMN personalize boolean NOT NULL DEFAULT false;

UPDATE discover_sections SET scope = 'genre', genre_names = ARRAY['Comedy'], personalize = true WHERE key = 'comedy_picks_for_you';
UPDATE discover_sections SET scope = 'genre', genre_names = ARRAY['Fantasy'], personalize = true WHERE key = 'fantasy_recommended_for_you';
UPDATE discover_sections SET scope = 'genre', genre_names = ARRAY['Mafia'], personalize = true WHERE key = 'mafia_burning_in_love';
UPDATE discover_sections SET scope = 'genre', genre_names = ARRAY['Romance'], personalize = true WHERE key = 'romance_recommended_for_you';
UPDATE discover_sections SET scope = 'genre', genre_names = ARRAY['Suspense'], personalize = true WHERE key = 'suspense_your_next_obsession';
UPDATE discover_sections SET scope = 'genre', genre_names = ARRAY['Werewolf'], personalize = true WHERE key = 'werewolf_picks_for_you';
