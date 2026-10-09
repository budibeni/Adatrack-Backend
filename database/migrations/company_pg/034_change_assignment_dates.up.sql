ALTER TABLE tm_driver_vehicles ALTER COLUMN assigned_at TYPE DATE USING assigned_at::DATE;
ALTER TABLE tm_driver_vehicles ALTER COLUMN unassigned_at TYPE DATE USING unassigned_at::DATE;
ALTER TABLE th_route_assignments RENAME COLUMN end_time TO end_date;
ALTER TABLE th_route_assignments ALTER COLUMN end_date TYPE DATE USING end_date::DATE;
