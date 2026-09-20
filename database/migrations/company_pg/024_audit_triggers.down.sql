DROP TRIGGER IF EXISTS trg_prevent_audit_delete ON th_audit_logs;
DROP TRIGGER IF EXISTS trg_prevent_audit_update ON th_audit_logs;
DROP FUNCTION IF EXISTS prevent_audit_update_delete();
