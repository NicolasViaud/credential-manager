package store

import "wcm/internal/model"

// CredentialStore is the strategy interface for credential persistence.
// Swap implementations to add Vault, database, or other backends.
type CredentialStore interface {
	Create(cred *model.Credential) error
	Get(userID, id string) (*model.Credential, error)
	Search(userID, collection string, attributes map[string]string) ([]*model.Credential, error)
	Update(cred *model.Credential) error
	Delete(userID, id string) error
}
