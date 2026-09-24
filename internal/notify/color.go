package notify

import (
	"fmt"
	"regexp"

	"github.com/trondhindenes/caramba/internal/config"
	"github.com/trondhindenes/caramba/internal/route"
)

// Automatic colors, matching Grafana's own Slack notifications.
const (
	colorFiring   = "#D63232"
	colorResolved = "#36A64F"
)

// colorizer picks an attachment color: the first config color rule whose
// pattern matches the alert title, otherwise one based on the status.
type colorizer struct {
	rules []compiledColor
}

type compiledColor struct {
	title *regexp.Regexp
	color string
}

func newColorizer(rules []config.ColorRule) (*colorizer, error) {
	c := &colorizer{}
	for i, r := range rules {
		re, err := route.Glob(r.Title)
		if err != nil {
			return nil, fmt.Errorf("colors[%d]: %w", i, err)
		}
		c.rules = append(c.rules, compiledColor{title: re, color: r.Color})
	}
	return c, nil
}

func (c *colorizer) color(msg *Message) string {
	for _, r := range c.rules {
		if r.title.MatchString(msg.AlertTitle) {
			return r.color
		}
	}
	switch msg.Status {
	case "firing":
		return colorFiring
	case "resolved":
		return colorResolved
	}
	return ""
}
