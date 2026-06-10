// Package model defines the data structures caramba persists and renders.
package model

import "time"

// Payload mirrors the Grafana alerting webhook contract: one delivery per
// alert group. The raw JSON bytes are persisted verbatim; this struct only
// has to cover what the GUI and rule matching need.
type Payload struct {
	Receiver          string            `json:"receiver"`
	Status            string            `json:"status"`
	OrgID             int64             `json:"orgId"`
	Alerts            []Alert           `json:"alerts"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL       string            `json:"externalURL"`
	Version           string            `json:"version"`
	GroupKey          string            `json:"groupKey"`
	TruncatedAlerts   int               `json:"truncatedAlerts"`
	Title             string            `json:"title"`
	State             string            `json:"state"`
	Message           string            `json:"message"`
}

type Alert struct {
	Status       string             `json:"status"`
	Labels       map[string]string  `json:"labels"`
	Annotations  map[string]string  `json:"annotations"`
	StartsAt     time.Time          `json:"startsAt"`
	EndsAt       time.Time          `json:"endsAt"`
	GeneratorURL string             `json:"generatorURL"`
	Fingerprint  string             `json:"fingerprint"`
	SilenceURL   string             `json:"silenceURL"`
	DashboardURL string             `json:"dashboardURL"`
	PanelURL     string             `json:"panelURL"`
	Values       map[string]float64 `json:"values"`
	ValueString  string             `json:"valueString"`
}

func (p *Payload) FiringCount() int   { return p.countStatus("firing") }
func (p *Payload) ResolvedCount() int { return p.countStatus("resolved") }

func (p *Payload) countStatus(status string) int {
	n := 0
	for _, a := range p.Alerts {
		if a.Status == status {
			n++
		}
	}
	return n
}
