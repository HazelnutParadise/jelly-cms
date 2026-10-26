ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS search_title text,
    ADD COLUMN IF NOT EXISTS search_content text,
    ADD COLUMN IF NOT EXISTS search_excerpt text;
