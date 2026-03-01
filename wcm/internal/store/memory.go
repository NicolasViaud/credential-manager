package store

import (
	"fmt"
	"sync"
	"time"
	"wcm/internal/model"

	"github.com/google/uuid"
)

type MemoryStore struct {
	mu          sync.RWMutex
	credentials map[string]*model.Credential // key: credential ID
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		credentials: make(map[string]*model.Credential),
	}
}

func (s *MemoryStore) Create(cred *model.Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cred.Collection == "" {
		cred.Collection = "default"
	}
	if cred.Attributes == nil {
		cred.Attributes = make(map[string]string)
	}

	cred.ID = uuid.NewString()
	cred.CreatedAt = time.Now()
	cred.UpdatedAt = time.Now()
	s.credentials[cred.ID] = cred
	return nil
}

func (s *MemoryStore) Get(userID, id string) (*model.Credential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cred, ok := s.credentials[id]
	if !ok || cred.UserID != userID {
		return nil, fmt.Errorf("not found")
	}
	c := *cred
	return &c, nil
}

func (s *MemoryStore) Search(userID, collection string, attributes map[string]string) ([]*model.Credential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []*model.Credential
	for _, cred := range s.credentials {
		if cred.UserID != userID {
			continue
		}
		if collection != "" && cred.Collection != collection {
			continue
		}
		if matchesAttributes(cred, attributes) {
			c := *cred
			results = append(results, &c)
		}
	}
	return results, nil
}

func (s *MemoryStore) Update(cred *model.Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.credentials[cred.ID]
	if !ok || existing.UserID != cred.UserID {
		return fmt.Errorf("not found")
	}
	cred.CreatedAt = existing.CreatedAt
	cred.UpdatedAt = time.Now()
	s.credentials[cred.ID] = cred
	return nil
}

func (s *MemoryStore) Delete(userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cred, ok := s.credentials[id]
	if !ok || cred.UserID != userID {
		return fmt.Errorf("not found")
	}
	delete(s.credentials, id)
	return nil
}

func matchesAttributes(cred *model.Credential, attributes map[string]string) bool {
	for k, v := range attributes {
		if cred.Attributes[k] != v {
			return false
		}
	}
	return true
}
