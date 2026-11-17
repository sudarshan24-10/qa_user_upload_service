ALTER TABLE documents
    DROP COLUMN IF EXISTS failure_stage,
    DROP COLUMN IF EXISTS failed_at;
