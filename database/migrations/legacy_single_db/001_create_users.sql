-- ============================================================================
-- Migration: 001_create_users_table
-- ============================================================================
-- Users table storing user credentials and roles (enterprise-standard schema)
-- ---------------------------------------------------------------------------
-- Enterprise-standard identity fields aligned with SCIM / SaaS IAM:
--   username            — System login name (SCIM userName)
--   first_name          — Given / first name
--   last_name           — Family / last name
--   phone_number        — Phone number in E.164 format
--   email_verified      — Boolean: has the email been verified?
--   phone_verified      — Boolean: has the phone been verified?
--   mfa_enabled         — Boolean: is multi-factor authentication enabled?
--   password_changed_at — Timestamp of last password change
--   failed_login_attempts — Counter for account-lockout logic
--   locked_until        — Account lockout expiry
--   locale              — User preferred locale (i18n)
--   avatar_url          — Profile picture URL
--   created_by          — Audit: creator user id
--   updated_by          — Audit: last modifier user id
--   deleted_at          — Soft-delete timestamp
-- ============================================================================

CREATE TABLE IF NOT EXISTS users (
    id                  BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    username            VARCHAR(191) NOT NULL UNIQUE,
    email               VARCHAR(191) NOT NULL UNIQUE,
    password_hash       VARCHAR(255) NOT NULL,
    first_name          VARCHAR(100) NULL,               -- Given / first name
    last_name           VARCHAR(100) NULL,               -- Family / last name
    phone_number        VARCHAR(20)  NULL,               -- E.164 format
    role                ENUM('ADMIN', 'adatrack_MANAGER', 'OPERATOR', 'DRIVER') NOT NULL DEFAULT 'DRIVER',
    status              ENUM('ACTIVE', 'INACTIVE', 'SUSPENDED') NOT NULL DEFAULT 'ACTIVE',
    email_verified      BOOLEAN    NOT NULL DEFAULT FALSE,
    phone_verified      BOOLEAN    NOT NULL DEFAULT FALSE,
    mfa_enabled         BOOLEAN    NOT NULL DEFAULT FALSE,
    password_changed_at TIMESTAMP  NULL,
    failed_login_attempts INT      NOT NULL DEFAULT 0,
    locked_until        TIMESTAMP  NULL,
    locale              VARCHAR(10) NOT NULL DEFAULT 'id',
    avatar_url          VARCHAR(512) NULL,
    last_login_at       TIMESTAMP NULL,
    created_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    created_by          BIGINT UNSIGNED NULL,
    updated_by          BIGINT UNSIGNED NULL,
    deleted_at          TIMESTAMP NULL,
    PRIMARY KEY (id),
    INDEX idx_users_username (username),
    INDEX idx_users_email (email),
    INDEX idx_users_role (role),
    INDEX idx_users_status (status),
    INDEX idx_users_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of Migration 001
-- ============================================================================