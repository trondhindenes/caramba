package model

import "time"

// Template is a user-authored message template. Title and Body are both
// template sources rendered by the declared engine against one webhook
// payload.
type Template struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Engine    string    `json:"engine"` // "grafana" | "jinja2"
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	UpdatedAt time.Time `json:"updatedAt"`
}
