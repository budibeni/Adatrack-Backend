-- Down migration is empty or just deletes the specific ones, but since it's a seed correction, we can leave it empty or delete everything
DELETE FROM tm_menus;
DELETE FROM tm_modules;
