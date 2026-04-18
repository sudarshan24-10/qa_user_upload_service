-- 1. Create ENUM
CREATE TYPE document_status AS ENUM (
    'INITIATED',
    'UPLOADED',
    'PROCESSING',
    'COMPLETED',
    'FAILED'
);

-- 2. Create table
CREATE TABLE documents (
    id UUID PRIMARY KEY,

    user_id UUID NOT NULL,

    object_key TEXT NOT NULL UNIQUE,

    file_name TEXT NOT NULL,

    size BIGINT NOT NULL CHECK (size > 0),

    status document_status NOT NULL DEFAULT 'INITIATED',

    idempotency_key TEXT NOT NULL,

    content_type TEXT NOT NULL,

    metadata JSONB DEFAULT '{}'::jsonb,

    upload_expires_at TIMESTAMP NOT NULL,
    failed_reason TEXT DEFAULT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- 3. Unique constraint (idempotency)
CREATE UNIQUE INDEX idx_documents_idempotency 
ON documents (idempotency_key);

-- 4. Main query index
CREATE INDEX idx_documents_status_expiry 
ON documents (status, upload_expires_at);

-- 5. User query index
CREATE INDEX idx_documents_user_created 
ON documents (user_id, created_at DESC);