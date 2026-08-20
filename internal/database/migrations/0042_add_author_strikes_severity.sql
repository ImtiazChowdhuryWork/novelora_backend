-- Strikes were flat, undifferentiated notes with nothing computing off
-- them. Adding severity lets the admin panel show a real risk signal
-- (weighted by severity, not just a raw count) instead of treating a
-- typo-in-bio strike the same as a stolen-content strike. Existing
-- rows default to 'moderate' — the closest neutral guess with no way
-- to know their real severity in hindsight.

ALTER TABLE author_strikes ADD COLUMN severity text NOT NULL DEFAULT 'moderate';
ALTER TABLE author_strikes ADD CONSTRAINT author_strikes_severity_check
  CHECK (severity IN ('minor', 'moderate', 'severe'));
