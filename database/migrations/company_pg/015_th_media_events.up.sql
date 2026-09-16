CREATE TABLE IF NOT EXISTS th_media_events (
    id BIGSERIAL PRIMARY KEY,
    vehicle_id INT NOT NULL REFERENCES tm_vehicles(id),
    status VARCHAR(20) DEFAULT 'uploaded',
    storage_key VARCHAR(255) NOT NULL,
    content_type VARCHAR(50),
    media_type VARCHAR(20),
    size_bytes BIGINT,
    uploaded_at TIMESTAMP WITH TIME ZONE,
    expires_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
