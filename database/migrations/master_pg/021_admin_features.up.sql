CREATE TABLE IF NOT EXISTS adatrack_gps_master.tm_sim_cards (
    id SERIAL PRIMARY KEY,
    iccid VARCHAR(50) UNIQUE NOT NULL,
    phone_number VARCHAR(30) NOT NULL,
    provider VARCHAR(50) NOT NULL,
    status VARCHAR(20) DEFAULT 'Active',
    expiry_date TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS adatrack_gps_master.tm_broadcasts (
    id SERIAL PRIMARY KEY,
    title VARCHAR(150) NOT NULL,
    message TEXT NOT NULL,
    target_audience VARCHAR(50) DEFAULT 'all',
    status VARCHAR(20) DEFAULT 'sent',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS adatrack_gps_master.tm_global_audit_logs (
    id SERIAL PRIMARY KEY,
    company_code VARCHAR(50) NOT NULL,
    actor_email VARCHAR(100),
    actor_role VARCHAR(50),
    action VARCHAR(100) NOT NULL,
    outcome VARCHAR(50),
    detail TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
