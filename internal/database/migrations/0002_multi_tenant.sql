-- Multi-tenant node assignment table
-- Tracks which PadPro server node each bridge user is assigned to.
CREATE TABLE IF NOT EXISTS node_assignment (
    bridge_user  TEXT PRIMARY KEY REFERENCES bridge_user(matrix_user_id),
    node_id      TEXT NOT NULL,
    assigned_at  TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    last_active  TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    wechat_id    TEXT,
    login_state  SMALLINT DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_node_assignment_node ON node_assignment(node_id);
CREATE INDEX IF NOT EXISTS idx_node_assignment_login_state ON node_assignment(login_state);
