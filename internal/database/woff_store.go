package database

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// WOFFUser represents an OAuth user (WOFF or LINE) stored in the database
type WOFFUser struct {
	UserID       string
	Provider     string // "woff" or "line"
	UserName     string
	DisplayName  string
	RefreshToken string
	Roles        []string
	DeletedAt    *time.Time // 論理削除用（NULLなら有効）
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// WOFFStore manages WOFF user data in SQLite
type WOFFStore struct {
	db *DB
}

// NewWOFFStore creates a new WOFFStore
func NewWOFFStore(db *DB) *WOFFStore {
	return &WOFFStore{db: db}
}

// SaveUser saves or updates a WOFF user
func (s *WOFFStore) SaveUser(user *WOFFUser) error {
	tx, err := s.db.conn.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Check if this is the first user (make them admin)
	// 削除済みユーザーを除外してカウント
	var userCount int
	err = tx.QueryRow("SELECT COUNT(*) FROM woff_users WHERE deleted_at IS NULL").Scan(&userCount)
	if err != nil {
		return fmt.Errorf("failed to count users: %w", err)
	}

	// デフォルトプロバイダー設定
	provider := user.Provider
	if provider == "" {
		provider = "woff"
	}

	// Upsert user
	query := `
		INSERT INTO woff_users (user_id, provider, user_name, display_name, refresh_token, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(user_id) DO UPDATE SET
			provider = excluded.provider,
			user_name = excluded.user_name,
			display_name = excluded.display_name,
			refresh_token = excluded.refresh_token,
			updated_at = CURRENT_TIMESTAMP
	`

	_, err = tx.Exec(query, user.UserID, provider, user.UserName, user.DisplayName, user.RefreshToken)
	if err != nil {
		return fmt.Errorf("failed to save user: %w", err)
	}

	// Delete existing roles
	_, err = tx.Exec("DELETE FROM woff_user_roles WHERE user_id = ?", user.UserID)
	if err != nil {
		return fmt.Errorf("failed to delete old roles: %w", err)
	}

	// Insert new roles
	roles := user.Roles
	if len(roles) == 0 {
		if userCount == 0 {
			// 最初のユーザーはadmin
			roles = []string{RoleAdmin}
		} else {
			// 以降のユーザーはデフォルトロール
			roles = []string{GetDefaultRole()}
		}
	}

	roleStmt, err := tx.Prepare("INSERT INTO woff_user_roles (user_id, role) VALUES (?, ?)")
	if err != nil {
		return fmt.Errorf("failed to prepare role statement: %w", err)
	}
	defer roleStmt.Close()

	for _, role := range roles {
		// 有効なロールのみ保存
		if IsValidRole(role) {
			_, err = roleStmt.Exec(user.UserID, role)
			if err != nil {
				return fmt.Errorf("failed to insert role: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// GetUser retrieves a user by user ID (deleted_at IS NULL のみ)
func (s *WOFFStore) GetUser(userID string) (*WOFFUser, error) {
	query := `
		SELECT user_id, provider, user_name, display_name, refresh_token, deleted_at, created_at, updated_at
		FROM woff_users
		WHERE user_id = ? AND deleted_at IS NULL
	`

	var user WOFFUser
	err := s.db.conn.QueryRow(query, userID).Scan(
		&user.UserID,
		&user.Provider,
		&user.UserName,
		&user.DisplayName,
		&user.RefreshToken,
		&user.DeletedAt,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("user not found: %s", userID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	// Get roles
	roles, err := s.getUserRoles(userID)
	if err != nil {
		return nil, err
	}
	user.Roles = roles

	return &user, nil
}

// GetUserByUsername retrieves a user by username (deleted_at IS NULL のみ)
func (s *WOFFStore) GetUserByUsername(username string) (*WOFFUser, error) {
	query := `
		SELECT user_id, provider, user_name, display_name, refresh_token, deleted_at, created_at, updated_at
		FROM woff_users
		WHERE user_name = ? AND deleted_at IS NULL
	`

	var user WOFFUser
	err := s.db.conn.QueryRow(query, username).Scan(
		&user.UserID,
		&user.Provider,
		&user.UserName,
		&user.DisplayName,
		&user.RefreshToken,
		&user.DeletedAt,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("user not found: %s", username)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	// Get roles
	roles, err := s.getUserRoles(user.UserID)
	if err != nil {
		return nil, err
	}
	user.Roles = roles

	return &user, nil
}

// UpdateRefreshToken updates only the refresh token for a user
func (s *WOFFStore) UpdateRefreshToken(userID, refreshToken string) error {
	query := `
		UPDATE woff_users
		SET refresh_token = ?, updated_at = CURRENT_TIMESTAMP
		WHERE user_id = ?
	`

	result, err := s.db.conn.Exec(query, refreshToken, userID)
	if err != nil {
		return fmt.Errorf("failed to update refresh token: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("user not found: %s", userID)
	}

	return nil
}

// DeleteUser soft deletes a user (論理削除)
func (s *WOFFStore) DeleteUser(userID string) error {
	// ユーザーがadminロールを持っている場合、最後のadminでないか確認
	user, err := s.GetUser(userID)
	if err != nil {
		return err
	}

	if HasAdminRole(user.Roles) {
		if err := s.ensureAtLeastOneAdmin(userID); err != nil {
			return err
		}
	}

	query := `
		UPDATE woff_users
		SET deleted_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE user_id = ? AND deleted_at IS NULL
	`

	result, err := s.db.conn.Exec(query, userID)
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("user not found or already deleted: %s", userID)
	}

	return nil
}

// HardDeleteUser permanently deletes a user (物理削除)
func (s *WOFFStore) HardDeleteUser(userID string) error {
	query := "DELETE FROM woff_users WHERE user_id = ?"

	result, err := s.db.conn.Exec(query, userID)
	if err != nil {
		return fmt.Errorf("failed to hard delete user: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("user not found: %s", userID)
	}

	return nil
}

// RestoreUser restores a soft-deleted user (復元)
func (s *WOFFStore) RestoreUser(userID string) error {
	query := `
		UPDATE woff_users
		SET deleted_at = NULL, updated_at = CURRENT_TIMESTAMP
		WHERE user_id = ? AND deleted_at IS NOT NULL
	`

	result, err := s.db.conn.Exec(query, userID)
	if err != nil {
		return fmt.Errorf("failed to restore user: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("user not found or not deleted: %s", userID)
	}

	return nil
}

// ListUsers returns all active users (deleted_at IS NULL のみ)
func (s *WOFFStore) ListUsers(limit, offset int) ([]*WOFFUser, error) {
	return s.ListUsersWithDeleted(limit, offset, false)
}

// ListUsersWithDeleted returns users with optional inclusion of deleted users
func (s *WOFFStore) ListUsersWithDeleted(limit, offset int, includeDeleted bool) ([]*WOFFUser, error) {
	var query string
	if includeDeleted {
		query = `
			SELECT user_id, provider, user_name, display_name, refresh_token, deleted_at, created_at, updated_at
			FROM woff_users
			ORDER BY created_at DESC
			LIMIT ? OFFSET ?
		`
	} else {
		query = `
			SELECT user_id, provider, user_name, display_name, refresh_token, deleted_at, created_at, updated_at
			FROM woff_users
			WHERE deleted_at IS NULL
			ORDER BY created_at DESC
			LIMIT ? OFFSET ?
		`
	}

	rows, err := s.db.conn.Query(query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}
	defer rows.Close()

	var users []*WOFFUser
	for rows.Next() {
		var user WOFFUser
		err := rows.Scan(
			&user.UserID,
			&user.Provider,
			&user.UserName,
			&user.DisplayName,
			&user.RefreshToken,
			&user.DeletedAt,
			&user.CreatedAt,
			&user.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}

		// Get roles
		roles, err := s.getUserRoles(user.UserID)
		if err != nil {
			return nil, err
		}
		user.Roles = roles

		users = append(users, &user)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return users, nil
}

// CountUsers returns the total number of users
func (s *WOFFStore) CountUsers(includeDeleted bool) (int, error) {
	var query string
	if includeDeleted {
		query = "SELECT COUNT(*) FROM woff_users"
	} else {
		query = "SELECT COUNT(*) FROM woff_users WHERE deleted_at IS NULL"
	}

	var count int
	err := s.db.conn.QueryRow(query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count users: %w", err)
	}

	return count, nil
}

// getUserRoles retrieves roles for a user
func (s *WOFFStore) getUserRoles(userID string) ([]string, error) {
	query := "SELECT role FROM woff_user_roles WHERE user_id = ?"

	rows, err := s.db.conn.Query(query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get roles: %w", err)
	}
	defer rows.Close()

	var roles []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, fmt.Errorf("failed to scan role: %w", err)
		}
		roles = append(roles, role)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return roles, nil
}

// SearchUsers searches active users by username or display name
func (s *WOFFStore) SearchUsers(query string, limit int) ([]*WOFFUser, error) {
	searchQuery := `
		SELECT user_id, provider, user_name, display_name, refresh_token, deleted_at, created_at, updated_at
		FROM woff_users
		WHERE (user_name LIKE ? OR display_name LIKE ?) AND deleted_at IS NULL
		ORDER BY created_at DESC
		LIMIT ?
	`

	pattern := "%" + strings.ToLower(query) + "%"
	rows, err := s.db.conn.Query(searchQuery, pattern, pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to search users: %w", err)
	}
	defer rows.Close()

	var users []*WOFFUser
	for rows.Next() {
		var user WOFFUser
		err := rows.Scan(
			&user.UserID,
			&user.Provider,
			&user.UserName,
			&user.DisplayName,
			&user.RefreshToken,
			&user.DeletedAt,
			&user.CreatedAt,
			&user.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}

		// Get roles
		roles, err := s.getUserRoles(user.UserID)
		if err != nil {
			return nil, err
		}
		user.Roles = roles

		users = append(users, &user)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return users, nil
}

// AddRole adds a role to a user
func (s *WOFFStore) AddRole(userID, role string) error {
	if !IsValidRole(role) {
		return fmt.Errorf("invalid role: %s", role)
	}

	query := "INSERT OR IGNORE INTO woff_user_roles (user_id, role) VALUES (?, ?)"
	_, err := s.db.conn.Exec(query, userID, role)
	if err != nil {
		return fmt.Errorf("failed to add role: %w", err)
	}

	// Update user's updated_at timestamp
	_, err = s.db.conn.Exec("UPDATE woff_users SET updated_at = CURRENT_TIMESTAMP WHERE user_id = ?", userID)
	if err != nil {
		return fmt.Errorf("failed to update user timestamp: %w", err)
	}

	return nil
}

// RemoveRole removes a role from a user
func (s *WOFFStore) RemoveRole(userID, role string) error {
	// adminロールを削除しようとしている場合、最後のadminでないか確認
	if role == RoleAdmin {
		if err := s.ensureAtLeastOneAdmin(userID); err != nil {
			return err
		}
	}

	query := "DELETE FROM woff_user_roles WHERE user_id = ? AND role = ?"
	result, err := s.db.conn.Exec(query, userID, role)
	if err != nil {
		return fmt.Errorf("failed to remove role: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("role not found for user: %s", userID)
	}

	// Update user's updated_at timestamp
	_, err = s.db.conn.Exec("UPDATE woff_users SET updated_at = CURRENT_TIMESTAMP WHERE user_id = ?", userID)
	if err != nil {
		return fmt.Errorf("failed to update user timestamp: %w", err)
	}

	return nil
}

// SetRoles sets all roles for a user (replaces existing roles)
func (s *WOFFStore) SetRoles(userID string, roles []string) error {
	// 新しいロールにadminが含まれていない場合、最後のadminでないか確認
	hasAdmin := false
	for _, role := range roles {
		if role == RoleAdmin {
			hasAdmin = true
			break
		}
	}

	if !hasAdmin {
		if err := s.ensureAtLeastOneAdmin(userID); err != nil {
			return err
		}
	}

	tx, err := s.db.conn.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete existing roles
	_, err = tx.Exec("DELETE FROM woff_user_roles WHERE user_id = ?", userID)
	if err != nil {
		return fmt.Errorf("failed to delete old roles: %w", err)
	}

	// Insert new roles
	if len(roles) > 0 {
		roleStmt, err := tx.Prepare("INSERT INTO woff_user_roles (user_id, role) VALUES (?, ?)")
		if err != nil {
			return fmt.Errorf("failed to prepare role statement: %w", err)
		}
		defer roleStmt.Close()

		for _, role := range roles {
			if IsValidRole(role) {
				_, err = roleStmt.Exec(userID, role)
				if err != nil {
					return fmt.Errorf("failed to insert role: %w", err)
				}
			}
		}
	}

	// Update user's updated_at timestamp
	_, err = tx.Exec("UPDATE woff_users SET updated_at = CURRENT_TIMESTAMP WHERE user_id = ?", userID)
	if err != nil {
		return fmt.Errorf("failed to update user timestamp: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// ensureAtLeastOneAdmin checks if removing admin role from userID would leave no admins
func (s *WOFFStore) ensureAtLeastOneAdmin(excludeUserID string) error {
	query := `
		SELECT COUNT(DISTINCT user_id)
		FROM woff_user_roles
		WHERE role = ? AND user_id != ?
	`

	var adminCount int
	err := s.db.conn.QueryRow(query, RoleAdmin, excludeUserID).Scan(&adminCount)
	if err != nil {
		return fmt.Errorf("failed to count admins: %w", err)
	}

	if adminCount == 0 {
		return fmt.Errorf("cannot remove admin role: at least one admin must exist")
	}

	return nil
}

// ListDeletedUsers returns all deleted users
func (s *WOFFStore) ListDeletedUsers(limit, offset int) ([]*WOFFUser, error) {
	query := `
		SELECT user_id, provider, user_name, display_name, refresh_token, deleted_at, created_at, updated_at
		FROM woff_users
		WHERE deleted_at IS NOT NULL
		ORDER BY deleted_at DESC
		LIMIT ? OFFSET ?
	`

	rows, err := s.db.conn.Query(query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list deleted users: %w", err)
	}
	defer rows.Close()

	var users []*WOFFUser
	for rows.Next() {
		var user WOFFUser
		err := rows.Scan(
			&user.UserID,
			&user.Provider,
			&user.UserName,
			&user.DisplayName,
			&user.RefreshToken,
			&user.DeletedAt,
			&user.CreatedAt,
			&user.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}

		// Get roles
		roles, err := s.getUserRoles(user.UserID)
		if err != nil {
			return nil, err
		}
		user.Roles = roles

		users = append(users, &user)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return users, nil
}

// GetUsersByRole returns all active users with a specific role
func (s *WOFFStore) GetUsersByRole(role string, limit, offset int) ([]*WOFFUser, error) {
	if !IsValidRole(role) {
		return nil, fmt.Errorf("invalid role: %s", role)
	}

	query := `
		SELECT DISTINCT u.user_id, u.provider, u.user_name, u.display_name, u.refresh_token, u.deleted_at, u.created_at, u.updated_at
		FROM woff_users u
		INNER JOIN woff_user_roles r ON u.user_id = r.user_id
		WHERE r.role = ? AND u.deleted_at IS NULL
		ORDER BY u.created_at DESC
		LIMIT ? OFFSET ?
	`

	rows, err := s.db.conn.Query(query, role, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get users by role: %w", err)
	}
	defer rows.Close()

	var users []*WOFFUser
	for rows.Next() {
		var user WOFFUser
		err := rows.Scan(
			&user.UserID,
			&user.Provider,
			&user.UserName,
			&user.DisplayName,
			&user.RefreshToken,
			&user.DeletedAt,
			&user.CreatedAt,
			&user.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}

		// Get all roles for this user
		roles, err := s.getUserRoles(user.UserID)
		if err != nil {
			return nil, err
		}
		user.Roles = roles

		users = append(users, &user)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return users, nil
}
