-- Ensure any legacy modules re-inserted by old 03_seed_master.sql are removed
DELETE FROM tm_menus WHERE module_id IN (SELECT id FROM tm_modules WHERE code LIKE 'business.%');
DELETE FROM tm_modules WHERE code LIKE 'business.%';
