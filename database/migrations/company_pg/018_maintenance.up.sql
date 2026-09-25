CREATE TABLE tm_maintenance_tasks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_code VARCHAR(50) NOT NULL,
    vehicle_id INT NOT NULL REFERENCES tm_vehicles(id) ON DELETE CASCADE,
    task_name VARCHAR(255) NOT NULL,
    description TEXT,
    interval_km FLOAT,
    interval_hours FLOAT,
    last_service_km FLOAT,
    last_service_hours FLOAT,
    last_service_date TIMESTAMP WITH TIME ZONE,
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE th_maintenance_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_code VARCHAR(50) NOT NULL,
    vehicle_id INT NOT NULL REFERENCES tm_vehicles(id) ON DELETE CASCADE,
    task_id UUID REFERENCES tm_maintenance_tasks(id) ON DELETE SET NULL,
    service_date TIMESTAMP WITH TIME ZONE NOT NULL,
    service_km FLOAT,
    service_hours FLOAT,
    cost DECIMAL(15,2),
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
