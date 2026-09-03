package domain

import "time"

type PromptTemplate struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Layer       string    `json:"layer"`
	Description string    `json:"description"`
	Content     string    `json:"content"`
	Source      string    `json:"source"`
	Editable    bool      `json:"editable"`
	Required    bool      `json:"required"`
	Version     int       `json:"version"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}
