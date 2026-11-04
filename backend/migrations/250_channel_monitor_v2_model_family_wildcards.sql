-- Append model-family prefix patterns (e.g. "claude-*") to every non-empty
-- channel_monitor_v2 allow-list. The lists seeded by 197 are exact names, so
-- each new release (claude-opus-5-5, gpt-6.1-sol, ...) was shown as __other__
-- until an operator listed it by hand. A trailing "*" is a prefix pattern; a
-- matching model keeps its real name.
--
-- Empty lists already show every real model and are left alone, as are
-- platforms without a family mapping and patterns that are already present.

UPDATE channel_monitor_v2_config AS c
SET
    platforms = patched.platforms,
    version = c.version + 1,
    updated_at = NOW()
FROM (
    SELECT
        cfg.id,
        jsonb_agg(
            CASE
                WHEN fam.patterns IS NOT NULL
                    AND jsonb_typeof(t.elem -> 'models') = 'array'
                    AND jsonb_array_length(t.elem -> 'models') > 0
                THEN jsonb_set(
                    t.elem,
                    '{models}',
                    (t.elem -> 'models') || (
                        SELECT COALESCE(jsonb_agg(p.pattern), '[]'::jsonb)
                        FROM jsonb_array_elements(fam.patterns) AS p(pattern)
                        WHERE NOT (t.elem -> 'models') @> jsonb_build_array(p.pattern)
                    )
                )
                ELSE t.elem
            END
            ORDER BY t.ord
        ) AS platforms
    FROM channel_monitor_v2_config AS cfg
    CROSS JOIN LATERAL jsonb_array_elements(cfg.platforms) WITH ORDINALITY AS t(elem, ord)
    LEFT JOIN (
        VALUES
            ('anthropic', '["claude-*"]'::jsonb),
            ('kiro', '["claude-*"]'::jsonb),
            ('antigravity', '["claude-*", "gemini-*"]'::jsonb),
            ('openai', '["gpt-*"]'::jsonb),
            ('grok', '["grok-*"]'::jsonb),
            ('gemini', '["gemini-*"]'::jsonb)
    ) AS fam(platform, patterns) ON fam.platform = t.elem ->> 'platform'
    WHERE cfg.id = 1
        AND jsonb_typeof(cfg.platforms) = 'array'
    GROUP BY cfg.id
) AS patched
WHERE c.id = patched.id
    AND c.platforms IS DISTINCT FROM patched.platforms;
