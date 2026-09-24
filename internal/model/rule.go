package model

import "time"

// Rule routes matching webhook deliveries: the first rule (in list order)
// whose matchers all match renders its template and sends the result to
// each of its destinations. A rule without matchers matches everything, so
// a catch-all default belongs last. A rule without destinations drops
// matching alerts.
type Rule struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Matchers     []Matcher `json:"matchers"`
	TemplateID   string    `json:"templateId"`
	Destinations []string  `json:"destinations"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// Matcher compares a case-insensitive wildcard pattern against either the
// payload title (Field "title") or the value at a dotted JSON path into the
// payload, such as "commonLabels.namespace".
type Matcher struct {
	Field   string `json:"field"`
	Pattern string `json:"pattern"`
}

// Dispatch records how one webhook delivery was routed and sent.
type Dispatch struct {
	At       time.Time        `json:"at"`
	RuleID   string           `json:"ruleId,omitempty"`
	RuleName string           `json:"ruleName,omitempty"`
	Error    string           `json:"error,omitempty"` // failure before sending, e.g. rendering
	Results  []DeliveryResult `json:"results,omitempty"`
}

type DeliveryResult struct {
	Destination string `json:"destination"`
	Attempts    int    `json:"attempts"`
	Error       string `json:"error,omitempty"`
}
