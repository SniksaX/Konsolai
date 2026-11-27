ALTER TABLE actions DROP CONSTRAINT IF EXISTS actions_decision_check;
ALTER TABLE actions ADD CONSTRAINT actions_decision_check CHECK (decision IN (
  'approved', 'approved_all', 'auto', 'rejected', 'gate_blocked', 'invalid',
  'code_running_approved', 'code_running_rejected', 'code_running_refused'
));
