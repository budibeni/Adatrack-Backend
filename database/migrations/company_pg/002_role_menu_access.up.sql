CREATE TABLE IF NOT EXISTS tm_role_menu_access (
    id SERIAL PRIMARY KEY,
    role VARCHAR(50) NOT NULL,
    menu_id INT NOT NULL, 
    can_view BOOLEAN DEFAULT true,
    can_create BOOLEAN DEFAULT false,
    can_edit BOOLEAN DEFAULT false,
    can_delete BOOLEAN DEFAULT false,
    enabled BOOLEAN DEFAULT true,
    deleted_at TIMESTAMP WITH TIME ZONE
);
