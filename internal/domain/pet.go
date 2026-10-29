package domain

import "time"

// PetState is the owner-scoped, server-authoritative state of the web pet.
// Position is stored as a normalized viewport coordinate plus a semantic zone
// so desktop and mobile clients can render the same pet without sharing pixels.
type PetState struct {
	ID        string    `json:"id"`
	Character string    `json:"character"`
	Mood      string    `json:"mood"`
	Action    string    `json:"action"`
	Zone      string    `json:"zone"`
	X         float64   `json:"x"`
	Y         float64   `json:"y"`
	Facing    int       `json:"facing"`
	Sleeping  bool      `json:"sleeping"`
	Quiet     bool      `json:"quiet"`
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

type PetStatePatch struct {
	Mood     *string  `json:"mood,omitempty"`
	Action   *string  `json:"action,omitempty"`
	Zone     *string  `json:"zone,omitempty"`
	X        *float64 `json:"x,omitempty"`
	Y        *float64 `json:"y,omitempty"`
	Facing   *int     `json:"facing,omitempty"`
	Sleeping *bool    `json:"sleeping,omitempty"`
	Quiet    *bool    `json:"quiet,omitempty"`
	Version  int64    `json:"version,omitempty"`
}
