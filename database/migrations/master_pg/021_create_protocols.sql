-- ============================================================================
-- Migration: MASTER 021 — tm_protocols (universal protocol/brand registry, B11)
-- ============================================================================
-- PRD Module 1c: the platform accepts EVERY GPS brand. This master reference
-- table mirrors the Go registry `internal/protocol` so the dashboard can list
-- supported brands/protocols and api-vehicle can validate a device registration
-- against a persisted catalogue (a device is never registered on a listener
-- that does not exist).
--
-- Reference data is append-only in practice: rows are upserted by the seed below
-- (ON CONFLICT DO UPDATE) and never hard deleted.
-- ============================================================================

CREATE TABLE IF NOT EXISTS tm_protocols (
    code VARCHAR(40) PRIMARY KEY,
    name VARCHAR(120) NOT NULL,
    brand VARCHAR(80) NOT NULL,
    transport VARCHAR(8) NOT NULL DEFAULT 'tcp' CHECK (transport IN ('tcp', 'udp')),
    default_port INT NOT NULL DEFAULT 0 CHECK (default_port >= 0),
    env_key VARCHAR(64),
    decoder VARCHAR(40) NOT NULL,
    reference VARCHAR(16) NOT NULL DEFAULT 'traccar' CHECK (reference IN ('traccar', 'own')),
    status VARCHAR(16) NOT NULL DEFAULT 'partial' CHECK (status IN ('full', 'partial', 'framing')),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    notes TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_tm_protocols_brand ON tm_protocols (brand);
CREATE INDEX IF NOT EXISTS idx_tm_protocols_status ON tm_protocols (status);

-- Seed: the listener/brand table of `services/ingestion-tcp` (B0–B9). Idempotent
-- so re-applying after a code change reconciles the registry.
INSERT INTO tm_protocols (code, name, brand, transport, default_port, env_key, decoder, reference, status, notes) VALUES
    ('gt06',      'GT06 / Concox',                'Concox',    'tcp', 5001, 'TCP_PORT',           'gt06',      'traccar', 'full',    'Listener utama (PRD Module 1a).'),
    ('teltonika', 'Teltonika Codec 8 / 8E',       'Teltonika', 'tcp', 5027, 'TELTONIKA_TCP_PORT', 'teltonika', 'own',     'full',    'Satu-satunya protokol dengan referensi sendiri (PRD Module 1b).'),
    ('tk103',     'TK103 (GT-clone)',             'TK103',     'tcp', 5013, 'TK103_TCP_PORT',     'tk103',     'traccar', 'partial', 'Login+handshake BP00/BP05, posisi, odometer, 7 perintah downlink.'),
    ('meiligao',  'Meiligao GT30i/GT60/VT300',    'Meiligao',  'tcp', 5002, 'MEILIGAO_TCP_PORT',  'meiligao',  'traccar', 'partial', 'Login/heartbeat/posisi/alarm; OBD/DTC/RFID belum.'),
    ('xexun',     'Xexun GPS103/GPS303',          'Xexun',     'tcp', 5003, 'XEXUN_TCP_PORT',     'xexun',     'traccar', 'full',    'NMEA basic + full, E2E terbukti.'),
    ('suntech',   'Suntech ST215/ST240/ST340',    'Suntech',   'tcp', 5017, 'SUNTECH_TCP_PORT',   'suntech',   'traccar', 'partial', 'Teks universal klasik; varian biner/per-model belum.'),
    ('h02',       'H02 / H08',                    'H02',       'tcp', 5010, 'H02_TCP_PORT',       'h02',       'traccar', 'partial', 'Teks V0/HTBT/V3; biner belum.'),
    ('totem',     'Totem',                        'Totem',     'tcp', 5005, 'TOTEM_TCP_PORT',     'totem',     'traccar', 'partial', 'PATTERN_1 + PATTERN_2.'),
    ('gt02',      'GT02',                         'GT02',      'tcp', 5006, 'GT02_TCP_PORT',      'gt02',      'traccar', 'full',    'Posisi + heartbeat.'),
    ('navigil',   'Navigil',                      'Navigil',   'tcp', 5012, 'NAVIGIL_TCP_PORT',   'navigil',   'traccar', 'partial', 'MSG 8/18; MSG 13/15 belum.'),
    ('castel',    'Castel',                       'Castel',    'tcp', 5019, 'CASTEL_TCP_PORT',    'castel',    'traccar', 'framing', 'Framing+respons+identitas; decoding GPS opt-in CASTEL_GPS_DECODE.')
ON CONFLICT (code) DO UPDATE SET
    name = EXCLUDED.name,
    brand = EXCLUDED.brand,
    transport = EXCLUDED.transport,
    default_port = EXCLUDED.default_port,
    env_key = EXCLUDED.env_key,
    decoder = EXCLUDED.decoder,
    reference = EXCLUDED.reference,
    status = EXCLUDED.status,
    notes = EXCLUDED.notes,
    updated_at = CURRENT_TIMESTAMP;
