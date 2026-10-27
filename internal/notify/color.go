package notify

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/trondhindenes/caramba/internal/config"
	"github.com/trondhindenes/caramba/internal/route"
)

// Automatic colors, matching Grafana's own Slack notifications.
const (
	colorFiring   = "#D63232"
	colorResolved = "#36A64F"
)

// colorizer picks a message color: the first config color rule whose
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

// cardStyle maps a color to the nearest Adaptive Card container style,
// since Teams cards can't take arbitrary colors.
func cardStyle(color string) string {
	switch color {
	case "":
		return "default"
	case "good":
		return "good"
	case "warning":
		return "warning"
	case "danger":
		return "attention"
	}
	rgb, err := strconv.ParseUint(color[1:], 16, 32)
	if err != nil {
		return "default"
	}
	r, g, b := float64(rgb>>16&0xff), float64(rgb>>8&0xff), float64(rgb&0xff)
	hi, lo := max(r, g, b), min(r, g, b)
	if hi == 0 || (hi-lo)/hi < 0.25 {
		return "emphasis" // grayish
	}
	var hue float64
	switch d := hi - lo; hi {
	case r:
		hue = 60 * (g - b) / d
	case g:
		hue = 60 * (2 + (b-r)/d)
	default:
		hue = 60 * (4 + (r-g)/d)
	}
	if hue < 0 {
		hue += 360
	}
	switch {
	case hue < 20 || hue >= 330:
		return "attention"
	case hue < 70:
		return "warning"
	case hue < 170:
		return "good"
	}
	return "accent"
}
