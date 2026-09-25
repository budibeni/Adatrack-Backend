CREATE OR REPLACE FUNCTION prevent_audit_update_delete()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'Audit logs are append-only. UPDATE and DELETE operations are not allowed.';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_prevent_audit_update
BEFORE UPDATE ON th_audit_logs
FOR EACH ROW EXECUTE FUNCTION prevent_audit_update_delete();

CREATE TRIGGER trg_prevent_audit_delete
BEFORE DELETE ON th_audit_logs
FOR EACH ROW EXECUTE FUNCTION prevent_audit_update_delete();
