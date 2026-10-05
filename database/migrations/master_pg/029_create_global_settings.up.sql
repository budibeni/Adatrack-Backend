CREATE TABLE IF NOT EXISTS tm_global_settings (
    id SERIAL PRIMARY KEY,
    setting_key VARCHAR(100) UNIQUE NOT NULL,
    setting_value JSONB NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO tm_global_settings (setting_key, setting_value) 
VALUES ('smtp_config', '{"host": "", "port": 587, "username": "", "password": "", "from_email": "", "from_name": "Adatrack System"}'::jsonb)
ON CONFLICT (setting_key) DO NOTHING;
