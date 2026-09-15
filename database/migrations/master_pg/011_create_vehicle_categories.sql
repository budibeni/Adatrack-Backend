-- ============================================================================
-- Migration: MASTER 011 — tm_vehicle_categories (master reference)
-- ============================================================================
-- Categories: PVB (passenger), LCV, HCV, TW, THW, EV, SPV (PRD §6.1).

CREATE TABLE IF NOT EXISTS tm_vehicle_categories (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code VARCHAR(20) NOT NULL,
    name VARCHAR(100) NOT NULL,
    description VARCHAR(255),
    sort_order INT NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_tm_vehicle_categories_code UNIQUE (code)
);

INSERT INTO tm_vehicle_categories (code, name, description, sort_order) VALUES
    ('PVB', 'Passenger Vehicle B (kendaraan penumpang)', 'Mobil penumpang / niaga ringan', 1),
    ('LCV', 'Light Commercial Vehicle', 'Kendaraan niaga ringan', 2),
    ('HCV', 'Heavy Commercial Vehicle', 'Kendaraan niaga berat / truk besar', 3),
    ('TW',  'Two Wheeler (motor)', 'Kendaraan bermotor 2 roda', 4),
    ('THW', 'Three Wheeler', 'Kendaraan bermotor 3 roda', 5),
    ('EV',  'Electric Vehicle', 'Kendaraan listrik', 6),
    ('SPV', 'Special Purpose Vehicle', 'Kendaraan khusus (ambulans, damkar, dll.)', 7)
ON CONFLICT (code) DO UPDATE SET
    name = EXCLUDED.name,
    description = EXCLUDED.description,
    sort_order = EXCLUDED.sort_order;