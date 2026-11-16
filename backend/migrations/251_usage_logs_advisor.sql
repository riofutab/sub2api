-- Anthropic advisor tool sub-inferences (usage.iterations[] type=advisor_message).
-- Top-level usage only covers the executor model; advisor usage is billed at the
-- advisor model's own rates and recorded here. advisor_cost is already included
-- in total_cost (before rate multiplier).
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS advisor_model VARCHAR(100),
    ADD COLUMN IF NOT EXISTS advisor_calls INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS advisor_input_tokens INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS advisor_output_tokens INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS advisor_cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS advisor_cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS advisor_cost DECIMAL(20,10) NOT NULL DEFAULT 0;
