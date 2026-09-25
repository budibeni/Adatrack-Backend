SET search_path TO adatrack_gps_master;
CREATE TABLE IF NOT EXISTS tm_company_media_config (
    id SERIAL PRIMARY KEY,
    company_code VARCHAR(50) NOT NULL REFERENCES tm_companies(code),
    bucket VARCHAR(100) NOT NULL,
    retention_days INT DEFAULT 30,
    max_file_mb INT DEFAULT 50,
    hmac_secret VARCHAR(255),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS tm_audit_logs (
    id BIGSERIAL PRIMARY KEY,
    action VARCHAR(100) NOT NULL,
    outcome VARCHAR(50) NOT NULL,
    actor_user_id INT,
    actor_email VARCHAR(100),
    actor_role VARCHAR(50),
    company_code VARCHAR(50),
    entity_type VARCHAR(50),
    entity_id VARCHAR(50),
    before_data JSONB,
    after_data JSONB,
    ip_address VARCHAR(50),
    user_agent TEXT,
    request_id VARCHAR(100),
    reason TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_audit_logs_company ON tm_audit_logs(company_code, created_at);
