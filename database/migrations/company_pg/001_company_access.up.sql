CREATE TABLE IF NOT EXISTS tm_user_company_access (
    id SERIAL PRIMARY KEY,
    user_id INT NOT NULL, 
    role_override VARCHAR(50),
    is_active BOOLEAN DEFAULT true,
    permissions JSONB,
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX IF NOT EXISTS idx_user_access_userid ON tm_user_company_access(user_id);
