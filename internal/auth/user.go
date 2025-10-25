package auth

import (
	"errors"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrUserNotFound      = errors.New("user not found")
	ErrInvalidCredential = errors.New("invalid credentials")
)

type User struct {
	ID           string
	Username     string
	Email        string
	PasswordHash string
	Roles        []string
}

// UserStore is a simple in-memory user store for demonstration
type UserStore struct {
	users map[string]*User
}

func NewUserStore() *UserStore {
	store := &UserStore{
		users: make(map[string]*User),
	}

	// Add a demo user
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	store.users["demo"] = &User{
		ID:           "1",
		Username:     "demo",
		Email:        "demo@example.com",
		PasswordHash: string(hashedPassword),
		Roles:        []string{"user", "admin"},
	}

	return store
}

// Authenticate validates user credentials
func (s *UserStore) Authenticate(username, password string) (*User, error) {
	user, exists := s.users[username]
	if !exists {
		return nil, ErrUserNotFound
	}

	err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))
	if err != nil {
		return nil, ErrInvalidCredential
	}

	return user, nil
}

// GetUserByID retrieves a user by ID
func (s *UserStore) GetUserByID(id string) (*User, error) {
	for _, user := range s.users {
		if user.ID == id {
			return user, nil
		}
	}
	return nil, ErrUserNotFound
}
