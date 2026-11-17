DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_name = 'documents'
          AND column_name = 'failure_reason'
    ) AND NOT EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_name = 'documents'
          AND column_name = 'failed_reason'
    ) THEN
        ALTER TABLE documents RENAME COLUMN failure_reason TO failed_reason;
    END IF;
END $$;
