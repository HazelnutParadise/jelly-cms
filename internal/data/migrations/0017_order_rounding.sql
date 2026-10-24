-- Add the whole-unit settlement adjustment without changing historical totals.
ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS rounding_adjustment BIGINT NOT NULL DEFAULT 0;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'orders'::regclass
          AND conname = 'orders_rounding_adjustment_range'
    ) THEN
        ALTER TABLE orders
            ADD CONSTRAINT orders_rounding_adjustment_range
            CHECK (rounding_adjustment >= -49 AND rounding_adjustment <= 50);
    END IF;
END
$$;
