-- 0041: split dependencies.depends_on_id into typed target columns
-- (depends_on_issue_id / depends_on_wisp_id / depends_on_external) with a
-- STORED generated depends_on_id and PK (issue_id, depends_on_id).
--
-- TABLE-REBUILD REWRITE (aegis-i42b). The original ran an in-place ALTER chain
-- including `ALTER TABLE dependencies DROP PRIMARY KEY` and column drops. On
-- the shared Dolt server (2.1.8/2.0.6) that chain reproducibly killed the
-- CONNECTION (EOF x3 -> invalid connection) on real-size databases
-- (aegis-tvin/aegis-lmi: hq v40 and bobbin v42 both crash-cycled the server,
-- taking every rig on the shared connection down with them). This version
-- never ALTERs a populated table: rows are copied out to a staging table, the
-- old table is dropped, the target shape is built EMPTY (the few ALTERs that
-- Dolt's ordering constraints force all run on zero rows), and the rows are
-- copied back in one INSERT..SELECT.
--
-- Copy-out/copy-in (rather than CREATE new + RENAME) is deliberate: FK
-- constraint names are database-global, so a side-by-side dependencies_new
-- could not carry the canonical fk_dep_* names while the old table exists,
-- and renaming afterwards would need per-FK ALTERs — the statement class this
-- rewrite exists to avoid. The staging table has no keys or constraints;
-- nothing references dependencies, so dropping it is FK-safe under
-- FOREIGN_KEY_CHECKS=0.
--
-- The CREATE below is a transcription of SHOW CREATE TABLE dependencies after
-- the ORIGINAL migration ran (Dolt 2.1.8) — byte-identical output is the
-- contract, so DBs migrated either way are indistinguishable and 0042..0052
-- compose unchanged. The backfill precedence matches the original UPDATE
-- order: 'external:%' prefix first, then wisp-id match, then issue-id match,
-- else external (unresolvable targets preserved as external).
DELETE FROM dolt_nonlocal_tables;
CALL DOLT_COMMIT('-Am', 'disable nonlocal tables for fk migrations');
SET FOREIGN_KEY_CHECKS = 0;

SET @needs_migrate = (
    SELECT IF(COUNT(*) = 0, 1, 0)
    FROM INFORMATION_SCHEMA.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'dependencies'
      AND COLUMN_NAME = 'depends_on_wisp_id'
);

-- Stage the existing rows with the target classification computed inline.
-- No PK/indexes/FKs on the staging table: it exists only for the round-trip.
SET @sql = IF(@needs_migrate = 1,
    'CREATE TABLE dependencies_migr_0041 (
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

SET @wisps_exists = (
    SELECT IF(COUNT(*) > 0, 1, 0)
    FROM INFORMATION_SCHEMA.TABLES
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME = 'wisps'
);

-- With a wisps table: wisp match takes precedence over issue match, exactly
-- as the original's UPDATE sequence did (wisp UPDATE ran before issue UPDATE,
-- each only filling rows the earlier passes left NULL).
SET @sql = IF(@needs_migrate = 1 AND @wisps_exists = 1,
    'INSERT INTO dependencies_migr_0041
        (issue_id, type, created_at, created_by, metadata, thread_id,
         depends_on_issue_id, depends_on_wisp_id, depends_on_external)
     SELECT d.issue_id, d.type, d.created_at, d.created_by, d.metadata, d.thread_id,
        CASE WHEN d.depends_on_id NOT LIKE ''external:%''
                  AND w.id IS NULL AND i.id IS NOT NULL
             THEN d.depends_on_id END,
        CASE WHEN d.depends_on_id NOT LIKE ''external:%''
                  AND w.id IS NOT NULL
             THEN d.depends_on_id END,
        CASE WHEN d.depends_on_id LIKE ''external:%''
                  OR (w.id IS NULL AND i.id IS NULL)
             THEN d.depends_on_id END
     FROM dependencies d
     LEFT JOIN wisps w ON w.id = d.depends_on_id
     LEFT JOIN issues i ON i.id = d.depends_on_id',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Without a wisps table (defensive parity with the original's @wisps_exists
-- guard): only issue/external classification.
SET @sql = IF(@needs_migrate = 1 AND @wisps_exists = 0,
    'INSERT INTO dependencies_migr_0041
        (issue_id, type, created_at, created_by, metadata, thread_id,
         depends_on_issue_id, depends_on_wisp_id, depends_on_external)
     SELECT d.issue_id, d.type, d.created_at, d.created_by, d.metadata, d.thread_id,
        CASE WHEN d.depends_on_id NOT LIKE ''external:%'' AND i.id IS NOT NULL
             THEN d.depends_on_id END,
        NULL,
        CASE WHEN d.depends_on_id LIKE ''external:%'' OR i.id IS NULL
             THEN d.depends_on_id END
     FROM dependencies d
     LEFT JOIN issues i ON i.id = d.depends_on_id',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF(@needs_migrate = 1, 'DROP TABLE dependencies', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Rebuild the table EMPTY, then shape it, then fill it. Two Dolt constraints
-- force this exact order (both verified on 2.1.8, not assumed):
--   1. An FK on the base column of a STORED generated column is rejected
--      (errno 1105) even inside CREATE TABLE — the original migration's
--      FK-before-generated-column discovery applies to CREATE too, so
--      fk_dep_issue_target must exist before depends_on_id does.
--   2. FK constraint names are database-global, so this CREATE cannot run
--      while the old table still holds fk_dep_issue — hence DROP first, with
--      the staging table holding every row until the rebuild completes (plus
--      Dolt's own commit history) as the crash-recovery path.
-- Every ALTER below runs against a table with ZERO rows, so none of them is
-- the heavy-rewrite/DROP-PK class that kills the shared server's connection —
-- that is the entire point of this rewrite.
SET @sql = IF(@needs_migrate = 1,
    'CREATE TABLE dependencies (
        issue_id VARCHAR(255) NOT NULL,
        type VARCHAR(32) NOT NULL DEFAULT ''blocks'',
        created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
        created_by VARCHAR(255) NOT NULL,
        metadata JSON DEFAULT (JSON_OBJECT()),
        thread_id VARCHAR(255) DEFAULT '''',
        depends_on_issue_id VARCHAR(255),
        depends_on_wisp_id VARCHAR(255),
        depends_on_external VARCHAR(255),
        KEY idx_dep_external_target (depends_on_external),
        KEY idx_dep_issue_target (depends_on_issue_id),
        KEY idx_dep_wisp_target (depends_on_wisp_id),
        KEY idx_dependencies_issue (issue_id),
        KEY idx_dependencies_thread (thread_id),
        CONSTRAINT fk_dep_issue FOREIGN KEY (issue_id) REFERENCES issues (id) ON DELETE CASCADE,
        CONSTRAINT fk_dep_issue_target FOREIGN KEY (depends_on_issue_id) REFERENCES issues (id) ON DELETE CASCADE ON UPDATE CASCADE,
        CONSTRAINT ck_dep_one_target CHECK (((depends_on_issue_id IS NOT NULL) + (depends_on_wisp_id IS NOT NULL) + (depends_on_external IS NOT NULL)) = 1)
    )',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF(@needs_migrate = 1,
    'ALTER TABLE dependencies ADD COLUMN depends_on_id VARCHAR(255) AS (COALESCE(depends_on_issue_id, depends_on_wisp_id, depends_on_external)) STORED',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF(@needs_migrate = 1,
    'ALTER TABLE dependencies ADD PRIMARY KEY (issue_id, depends_on_id)',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF(@needs_migrate = 1,
    'ALTER TABLE dependencies ADD INDEX idx_dep_type_target (type, depends_on_id)',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF(@needs_migrate = 1,
    'INSERT INTO dependencies
        (issue_id, type, created_at, created_by, metadata, thread_id,
         depends_on_issue_id, depends_on_wisp_id, depends_on_external)
     SELECT issue_id, type, created_at, created_by, metadata, thread_id,
            depends_on_issue_id, depends_on_wisp_id, depends_on_external
     FROM dependencies_migr_0041',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF(@needs_migrate = 1, 'DROP TABLE dependencies_migr_0041', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET FOREIGN_KEY_CHECKS = 1;
