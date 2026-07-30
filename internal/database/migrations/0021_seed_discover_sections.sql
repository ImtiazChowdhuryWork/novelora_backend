-- Seeds discover_sections to reproduce, exactly, the sections already
-- hardcoded in DiscoverCubit's Dart constants and each
-- `*_tab_content.dart` widget as of 2026-07 — a behavior-neutral
-- cutover, not a redesign. Covers the 6 dedicated genre-category tabs
-- (Comedy, Fantasy, Mafia, Romance, Suspense, Werewolf); Discover and New
-- are deliberately out of scope for this pass (their sections are
-- already global/automatic, or — for New's Best Newcomers/Picks For
-- You — slice a shared paginated gallery rather than being an
-- independent fetch, which doesn't fit this registry's one-row-per-fetch
-- shape).
INSERT INTO discover_sections
    (key, category, label, layout, scope, genre_names, sort, status_filter, recommended_filter, exclusive_filter, exclude_section_keys, position)
VALUES
    -- Comedy
    ('comedy_editors_pick', 'Comedy', E'Editor’s Pick', 'shelf', 'global', '{}', 'rating', '', true, NULL, '{}', 1),
    ('comedy_most_read', 'Comedy', 'Most Read', 'ranked_grid', 'genre', ARRAY['Comedy'], 'trending', '', NULL, NULL, '{}', 2),
    ('comedy_completed', 'Comedy', 'Completed', 'ranked_grid', 'global', '{}', 'views', 'completed', NULL, NULL, '{}', 3),
    ('comedy_picks_for_you', 'Comedy', 'Picks For You', 'picks_feed', 'global', '{}', 'new', '', NULL, NULL, '{}', 4),

    -- Fantasy
    ('fantasy_top_fantasy', 'Fantasy', 'Top Fantasy', 'ranked_grid', 'genre', ARRAY['Fantasy'], 'trending', '', NULL, NULL, '{}', 1),
    ('fantasy_editors_pick', 'Fantasy', E'Editor’s Pick', 'shelf', 'genre', ARRAY['Fantasy'], 'rating', '', true, NULL, '{}', 2),
    ('fantasy_exclusive', 'Fantasy', 'Exclusive', 'shelf', 'genre', ARRAY['Fantasy'], 'new', '', NULL, NULL, '{}', 3),
    ('fantasy_recommended_for_you', 'Fantasy', 'Recommended For You', 'picks_feed', 'global', '{}', 'new', '', NULL, NULL, '{}', 4),

    -- Mafia
    ('mafia_dark_romance', 'Mafia', 'Dark Romance', 'ranked_grid', 'genre', ARRAY['Mafia'], 'trending', '', NULL, NULL, '{}', 1),
    ('mafia_cold_as_you', 'Mafia', 'Cold As You', 'shelf', 'global', '{}', 'new', '', NULL, NULL, '{}', 2),
    ('mafia_locked_out_of_heaven', 'Mafia', 'Locked Out of Heaven', 'ranked_grid', 'multi_genre', ARRAY['Mafia', 'Romance'], 'trending', '', NULL, NULL, '{}', 3),
    ('mafia_thinking_about_you', 'Mafia', 'Thinking About You', 'shelf', 'multi_genre', ARRAY['Mafia', 'CEO', 'Revenge', 'Dark Romance', 'steamy', 'Rate to love'], 'new', '', NULL, NULL, '{}', 4),
    ('mafia_kiss_me_more', 'Mafia', 'Kiss Me More', 'shelf', 'multi_genre', ARRAY['Mafia', 'CEO', 'Revenge', 'Dark Romance', 'steamy', 'Rate to love'], 'new', '', NULL, NULL, ARRAY['mafia_thinking_about_you'], 5),
    ('mafia_burning_in_love', 'Mafia', 'Burning In Love', 'picks_feed', 'global', '{}', 'new', '', NULL, NULL, '{}', 6),

    -- Romance
    ('romance_love_on_top', 'Romance', 'Love On Top', 'ranked_grid', 'genre', ARRAY['Romance'], 'trending', '', NULL, NULL, '{}', 1),
    ('romance_editors_picks', 'Romance', E'Editor’s Picks', 'shelf', 'genre', ARRAY['Romance'], 'new', '', NULL, NULL, ARRAY['romance_love_on_top'], 2),
    ('romance_exclusive', 'Romance', 'Exclusive', 'shelf', 'global', '{}', 'new', '', NULL, NULL, ARRAY['romance_love_on_top', 'romance_editors_picks'], 3),
    ('romance_completed', 'Romance', 'Completed', 'ranked_grid', 'global', '{}', 'views', 'completed', NULL, NULL, '{}', 4),
    ('romance_recommended_for_you', 'Romance', 'Recommended For You', 'picks_feed', 'global', '{}', 'new', '', NULL, NULL, '{}', 5),

    -- Suspense
    ('suspense_edge_of_your_seat', 'Suspense', 'Edge of Your Seat', 'ranked_grid', 'genre', ARRAY['Suspense'], 'trending', '', NULL, NULL, '{}', 1),
    ('suspense_prime_suspects', 'Suspense', 'Prime Suspects', 'shelf', 'genre', ARRAY['Suspense'], 'rating', '', true, NULL, '{}', 2),
    ('suspense_fresh_evidence', 'Suspense', 'Fresh Evidence', 'shelf', 'genre', ARRAY['Suspense'], 'new', '', NULL, NULL, '{}', 3),
    ('suspense_case_closed', 'Suspense', 'Case Closed', 'ranked_grid', 'global', '{}', 'views', 'completed', NULL, NULL, '{}', 4),
    ('suspense_your_next_obsession', 'Suspense', 'Your Next Obsession', 'picks_feed', 'global', '{}', 'new', '', NULL, NULL, '{}', 5),

    -- Werewolf
    ('werewolf_most_read', 'Werewolf', 'Most Read', 'ranked_grid', 'multi_genre', ARRAY['Mystery', 'Drama', 'Werewolf', 'Adventure'], 'trending', '', NULL, NULL, '{}', 1),
    ('werewolf_editors_picks', 'Werewolf', E'Editor’s Picks', 'shelf', 'multi_genre', ARRAY['Drama', 'Romance', 'Dark Romance', 'Thriller', 'Werewolf', 'Steamy'], 'rating', '', true, NULL, '{}', 2),
    ('werewolf_completed', 'Werewolf', 'Completed', 'ranked_grid', 'multi_genre', ARRAY['Romance', 'Action', 'Mystery', 'Dark Romance', 'Steamy', 'Werewolf'], 'trending', '', NULL, NULL, '{}', 3),
    ('werewolf_picks_for_you', 'Werewolf', 'Picks For You', 'picks_feed', 'global', '{}', 'new', '', NULL, NULL, '{}', 4);
