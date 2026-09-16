CREATE SCHEMA IF NOT EXISTS adatrack_gps_master;
SET search_path TO adatrack_gps_master;

CREATE TABLE IF NOT EXISTS tm_companies (
    id SERIAL PRIMARY KEY,
    code VARCHAR(50) UNIQUE NOT NULL,
    name VARCHAR(100) NOT NULL,
    business_type VARCHAR(10) NOT NULL DEFAULT 'B2B',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS tm_users (
    id SERIAL PRIMARY KEY,
    company_id INT REFERENCES tm_companies(id),
    username VARCHAR(50) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    role VARCHAR(20) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS tm_modules (
    id SERIAL PRIMARY KEY,
    name VARCHAR(50) UNIQUE NOT NULL
);

CREATE TABLE IF NOT EXISTS tm_menus (
    id SERIAL PRIMARY KEY,
    module_id INT REFERENCES tm_modules(id),
    name VARCHAR(50) NOT NULL,
    path VARCHAR(100) NOT NULL
);

CREATE TABLE IF NOT EXISTS tm_user_vehicles (
    user_id INT REFERENCES tm_users(id),
    vehicle_id INT,
    PRIMARY KEY(user_id, vehicle_id)
);
