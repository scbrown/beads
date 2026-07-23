-- 0043: drop the generated depends_on_id column and re-key dependencies on a
-- surrogate id CHAR(36) primary key with typed UNIQUE keys.
--
-- TABLE-REBUILD REWRITE (aegis-i42b). The original ran `ALTER TABLE
-- dependencies DROP PRIMARY KEY` + `ADD COLUMN id ... PRIMARY KEY FIRST` in
-- place — the statement class that reproducibly kills the shared Dolt
-- server's connection (aegis-tvin/aegis-lmi). This version copies rows out,
-- drops the table, CREATEs the exact target schema in one statement, and
-- copies rows back in. See 0041 for why copy-out/copy-in beats
-- CREATE-new+RENAME (database-global FK names).
--
-- #4259 note (carried from the original): the surrogate id must have NO
-- random default — the application sets a deterministic id at every insert
-- site, and migration 0050 + the rekeyDependencyIDs backfill rewrite any
-- transient ids minted here. The rebuild INSERT mints UUID()s for existing
-- rows exactly as the original's transient DEFAULT (UUID()) did, and the
-- CREATE defines id with no default, so a freshly migrated clone reaches the
-- no-default schema directly (what the original achieved by ADD-then-DROP
-- DEFAULT).
--
-- FK parity with the original: fk_dep_issue / fk_dep_issue_target are
-- re-created only if they existed before the rebuild (the original's
-- @has_fk_issue / @has_fk_issue_target guards), via conditional CREATE
-- fragments.
SET FOREIGN_KEY_CHECKS = 0;

SET @needs_drop = (
    SELECT IF(COUNT(*) > 0, 1, 0)
    FROM INFORMATION_SCHEMA.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'dependencies'
      AND COLUMN_NAME = 'depends_on_id'
);

SET @has_fk_issue = (
    SELECT IF(COUNT(*) > 0, 1, 0)
    FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'dependencies'
      AND CONSTRAINT_NAME = 'fk_dep_issue'
);

SET @has_fk_issue_target = (
    SELECT IF(COUNT(*) > 0, 1, 0)
    FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'dependencies'
      AND CONSTRAINT_NAME = 'fk_dep_issue_target'
);

-- Stage the rows, minting the transient per-row id here (rewritten to the
-- deterministic key by rekeyDependencyIDs/0050, same as the original's
-- DEFAULT (UUID()) backfill behaviour).
SET @sql = IF(@needs_drop = 1,
    'CREATE TABLE dependencies_migr_0043 (
        id CHAR(36) NOT NULL,
        issue_id VARCHAR(255) NOT NULL,
        type VARCHAR(32) NOT NULL,
        created_at DATETIME NOT NULL,
        created_by VARCHAR(255) NOT NULL,
        metadata JSON,
        thread_id VARCHAR(255),
        depends_on_issue_id VARCHAR(255),
        depends_on_wisp_id VARCHAR(255),
        depends_on_external VARCHAR(255)
    )',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF(@needs_drop = 1,
    'INSERT INTO dependencies_migr_0043
        (id, issue_id, type, created_at, created_by, metadata, thread_id,
         depends_on_issue_id, depends_on_wisp_id, depends_on_external)
     SELECT UUID(), issue_id, type, created_at, created_by, metadata, thread_id,
            depends_on_issue_id, depends_on_wisp_id, depends_on_external
     FROM dependencies',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF(@needs_drop = 1, 'DROP TABLE dependencies', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Exact post-0043 target schema (transcribed from Dolt's own SHOW CREATE
-- TABLE after the original chain on 2.1.8), with the FK fragments included
-- only when the pre-rebuild table carried them.
SET @sql = IF(@needs_drop = 1,
    CONCAT(
    'CREATE TABLE dependencies (
        id CHAR(36) NOT NULL,
        issue_id VARCHAR(255) NOT NULL,
        type VARCHAR(32) NOT NULL DEFAULT ''blocks'',
        created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
        created_by VARCHAR(255) NOT NULL,
        metadata JSON DEFAULT (JSON_OBJECT()),
        thread_id VARCHAR(255) DEFAULT '''',
        depends_on_issue_id VARCHAR(255),
        depends_on_wisp_id VARCHAR(255),
        depends_on_external VARCHAR(255),
        PRIMARY KEY (id),
        KEY idx_dep_external_target (depends_on_external),
        KEY idx_dep_issue_target (depends_on_issue_id),
        KEY idx_dep_type_external (type, depends_on_external),
        KEY idx_dep_type_issue (type, depends_on_issue_id),
        KEY idx_dep_type_wisp (type, depends_on_wisp_id),
        KEY idx_dep_wisp_target (depends_on_wisp_id),
        KEY idx_dependencies_issue (issue_id),
        KEY idx_dependencies_thread (thread_id),
        UNIQUE KEY uk_dep_external_target (issue_id, depends_on_external),
        UNIQUE KEY uk_dep_issue_target (issue_id, depends_on_issue_id),
        UNIQUE KEY uk_dep_wisp_target (issue_id, depends_on_wisp_id)',
    IF(@has_fk_issue = 1,
       ',
        CONSTRAINT fk_dep_issue FOREIGN KEY (issue_id) REFERENCES issues (id) ON DELETE CASCADE ON UPDATE CASCADE',
       ''),
    IF(@has_fk_issue_target = 1,
       ',
        CONSTRAINT fk_dep_issue_target FOREIGN KEY (depends_on_issue_id) REFERENCES issues (id) ON DELETE CASCADE ON UPDATE CASCADE',
       ''),
    ',
        CONSTRAINT ck_dep_one_target CHECK (((depends_on_issue_id IS NOT NULL) + (depends_on_wisp_id IS NOT NULL) + (depends_on_external IS NOT NULL)) = 1)
    )'),
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF(@needs_drop = 1,
    'INSERT INTO dependencies
        (id, issue_id, type, created_at, created_by, metadata, thread_id,
         depends_on_issue_id, depends_on_wisp_id, depends_on_external)
     SELECT id, issue_id, type, created_at, created_by, metadata, thread_id,
            depends_on_issue_id, depends_on_wisp_id, depends_on_external
     FROM dependencies_migr_0043',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF(@needs_drop = 1, 'DROP TABLE dependencies_migr_0043', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- #4259 repair path, carried verbatim from the original: a DB that ran the
-- PRE-#4259 version of this migration (@needs_drop=0 today) may still carry
-- the per-clone-random DEFAULT (UUID()) on id. Strip it. The rebuild path
-- above never creates a default, so this is a no-op for fresh migrations —
-- it exists for already-migrated lineages. ALTER COLUMN ... DROP DEFAULT is
-- not in the connection-killing statement class (no PK/column churn).
SET @id_has_default = (
    SELECT IF(COUNT(*) > 0, 1, 0)
    FROM INFORMATION_SCHEMA.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'dependencies'
      AND COLUMN_NAME = 'id'
      AND COLUMN_DEFAULT IS NOT NULL
);
SET @sql = IF(@id_has_default = 1,
    'ALTER TABLE dependencies ALTER COLUMN id DROP DEFAULT',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET FOREIGN_KEY_CHECKS = 1;
