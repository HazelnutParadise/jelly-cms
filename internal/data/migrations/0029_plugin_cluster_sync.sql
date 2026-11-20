-- Ticket 37 (多台伺服器的外掛狀態保持一致)。多台伺服器共用同一個資料庫，
-- 外掛壓縮檔與變更序號放在這裡，讓每台都能重建自己的 data/plugins 副本。
-- 可重跑：所有語句都是 IF NOT EXISTS 或可重複執行。

-- 共用變更序號（R1）：會改變外掛狀態或擴充點輸出的操作各呼叫一次 nextval。
-- 用 SEQUENCE 而不是一列計數，讓頻繁的設定寫入不必搶同一列的鎖。跳號沒有關係。
CREATE SEQUENCE IF NOT EXISTS plugin_state_seq;

-- 壓縮檔（R3）：以（外掛 ID、解壓後目錄雜湊）為主鍵，寫入後不再修改。
-- 不對 plugins 加外鍵，測試與移除流程都可能先後出現沒有列的外掛 ID。
CREATE TABLE IF NOT EXISTS plugin_archives (
    plugin_id VARCHAR(64) NOT NULL,
    tree_sha256 CHAR(64) NOT NULL CHECK (tree_sha256 ~ '^[0-9a-f]{64}$'),
    zip_sha256 CHAR(64) NOT NULL CHECK (zip_sha256 ~ '^[0-9a-f]{64}$'),
    zip_size BIGINT NOT NULL CHECK (zip_size >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (plugin_id, tree_sha256)
);

-- 壓縮檔內容切成每段 1 MiB，依段落序號串流讀寫，記憶體不隨外掛大小增加。
CREATE TABLE IF NOT EXISTS plugin_archive_chunks (
    plugin_id VARCHAR(64) NOT NULL,
    tree_sha256 CHAR(64) NOT NULL,
    seq INTEGER NOT NULL CHECK (seq >= 0),
    data BYTEA NOT NULL,
    PRIMARY KEY (plugin_id, tree_sha256, seq),
    CONSTRAINT plugin_archive_chunks_archive_fkey FOREIGN KEY (plugin_id, tree_sha256)
        REFERENCES plugin_archives (plugin_id, tree_sha256) ON DELETE CASCADE
);

-- 目前使用的壓縮檔指標。NULL 代表升級前就裝好、尚未回填的外掛。
ALTER TABLE plugins
    ADD COLUMN IF NOT EXISTS archive_tree_sha256 CHAR(64);

-- 解除安裝的移除紀錄（R3）：其他台看到它就卸載並刪除本機目錄。重新安裝時刪除。
CREATE TABLE IF NOT EXISTS plugin_removals (
    plugin_id VARCHAR(64) PRIMARY KEY,
    removed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
