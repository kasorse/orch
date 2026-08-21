package domain

import "time"

type Task struct {
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}
