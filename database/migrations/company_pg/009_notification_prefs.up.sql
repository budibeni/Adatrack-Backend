CREATE TABLE IF NOT EXISTS tm_notification_preferences (
    id SERIAL PRIMARY KEY,
    user_id INT NOT NULL,
    alert_type VARCHAR(50) NOT NULL,
    channel VARCHAR(20) NOT NULL,
    enabled BOOLEAN DEFAULT true,
    min_severity VARCHAR(20) DEFAULT 'low'
);
