package model

import "time"

type Credential struct {
	ID         string            `json:"id"`
	UserID     string            `json:"userId"`
	Collection string            `json:"collection"`
	Label      string            `json:"label"`
	Secret     string            `json:"secret"`
	Attributes map[string]string `json:"attributes"`
	CreatedAt  time.Time         `json:"createdAt"`
	UpdatedAt  time.Time         `json:"updatedAt"`
}
