package database

// ユーザーロール定義
const (
	RoleAdmin      = "admin"
	RoleMaintainer = "maintainer"
	RoleUser       = "user"
)

// ValidRoles は有効なロールのリスト
var ValidRoles = []string{
	RoleAdmin,
	RoleMaintainer,
	RoleUser,
}

// IsValidRole checks if a role is valid
func IsValidRole(role string) bool {
	for _, r := range ValidRoles {
		if r == role {
			return true
		}
	}
	return false
}

// GetDefaultRole returns the default role for new users
func GetDefaultRole() string {
	return RoleUser
}

// HasAdminRole checks if user has admin role
func HasAdminRole(roles []string) bool {
	for _, role := range roles {
		if role == RoleAdmin {
			return true
		}
	}
	return false
}

// HasMaintainerRole checks if user has maintainer role
func HasMaintainerRole(roles []string) bool {
	for _, role := range roles {
		if role == RoleMaintainer {
			return true
		}
	}
	return false
}

// CanManageUsers checks if user can manage other users (admin or maintainer)
func CanManageUsers(roles []string) bool {
	return HasAdminRole(roles) || HasMaintainerRole(roles)
}
