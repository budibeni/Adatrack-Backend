-- ============================================================================
-- Migration: COMPANY 025 — driver score normalised per distance (B8, FR-2.7)
-- ============================================================================
-- Gap closed: the count-based score (migration 022) cannot distinguish a vehicle
-- that drove 3 km from one that drove 300 km with the same number of harsh events.
-- `th_driver_scores` therefore gains the day's travelled distance (summed from the
-- B7.2 `th_vehicle_trips` rows) plus the derived rate, and `score` becomes the
-- distance-normalised value whenever enough distance was driven:
--
--   * distance_km      — travelled distance of the scored period;
--   * events_per_100km — weighted penalty points per 100 km (column is filled by
--                        worker-alert, NULL when the distance is too small);
--   * score_by_counts  — the previous count-based score, kept for audit so an
--                        operator can see both numbers and why one was used.
--
-- DEFAULT 0 / NULL keeps existing rows valid (additive-only rule §1); the CHECKs
-- mirror the numeric bounds already used in migration 022.
-- ============================================================================

ALTER TABLE th_driver_scores
    ADD COLUMN IF NOT EXISTS distance_km NUMERIC(12, 3) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS events_per_100km NUMERIC(10, 3),
    ADD COLUMN IF NOT EXISTS score_by_counts NUMERIC(5, 2);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'th_driver_scores_distance_check'
    ) THEN
        ALTER TABLE th_driver_scores
            ADD CONSTRAINT th_driver_scores_distance_check
            CHECK (distance_km >= 0 AND (events_per_100km IS NULL OR events_per_100km >= 0)
                   AND (score_by_counts IS NULL OR (score_by_counts >= 0 AND score_by_counts <= 100)));
    END IF;
END $$;

-- Ops/reporting: "how far did this vehicle drive on the scored days".
CREATE INDEX IF NOT EXISTS idx_th_driver_scores_distance
    ON th_driver_scores (company_code, period_start DESC, distance_km DESC);
