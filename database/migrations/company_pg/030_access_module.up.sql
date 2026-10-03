CREATE TABLE IF NOT EXISTS tm_personel (
    id SERIAL PRIMARY KEY,
    name VARCHAR(150) NOT NULL,
    personel_type VARCHAR(50) NOT NULL,
    nik VARCHAR(50),
    phone VARCHAR(50),
    email VARCHAR(100),
    address TEXT,
    card_id INT REFERENCES tm_rfid_cards(id) ON DELETE SET NULL,
    status VARCHAR(50) DEFAULT 'ACTIVE',
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

-- Note: tm_rfid_cards already created in 020_phase_b12_enterprise.up.sql
-- We'll alter th_access_logs to add more context if needed, but the basic structure is there.
-- Let's ensure tm_rfid_cards has 'name' or alias if needed, or we just rely on card_number.
-- The frontend card payload has: uid, name, type, status, holderType, holderId.
-- Our existing tm_rfid_cards has: id, card_number, assigned_to_type, assigned_to_id, status.
-- Let's add 'name' and 'type' to tm_rfid_cards.

ALTER TABLE tm_rfid_cards ADD COLUMN IF NOT EXISTS name VARCHAR(150);
ALTER TABLE tm_rfid_cards ADD COLUMN IF NOT EXISTS type VARCHAR(50) DEFAULT 'RFID';

