# caramba usage guide

caramba receives Grafana alert webhooks (one delivery per alert group), stores them, and routes each delivery through **routing rules** to **templates** and **destinations**.

- **Templates** (title + body) render a delivery into a message. You can create and edit them with these tools.
- **Destinations** (Slack, Teams) are defined in caramba's config file by the operator. Rules refer to them by name.
- **Routing rules** pick, for each delivery, one template and the destinations to send it to. Rules are edited by the user on the **Rules** page of the caramba web GUI; you cannot read or change them through these tools. When a rule is needed, give the user the exact values to enter, and check your matchers against a real stored alert (`get_alert`) first.

## Routing rules

A rule has a name, matchers, one template and zero or more destinations.

### Ordering: first match wins

Rules are evaluated top to bottom in the order shown on the Rules page. The **first** rule whose matchers all match handles the delivery, and no later rule sees it. So:

- Put specific rules above general ones.
- A rule with no matchers matches everything; use one as the last rule, a catch-all default.
- A rule with no destinations drops the matching deliveries (useful to silence noise above the catch-all).
- To send one alert to two places, give one rule both destinations; a second matching rule further down would never run.

Resolved notifications are deliveries too and go through the same rules. Add `status = firing` to a rule to only route firing deliveries.

### Matchers

Matchers are entered one per line as `field = pattern`. **All** matchers of a rule must match (AND). There is no OR within a rule; use two rules with the same template and destinations. Lines starting with `#` are comments.

**field** is a dotted path into the delivery's JSON, using the JSON field names exactly as `get_alert` shows them (case-sensitive): `status`, `receiver`, `title`, `groupLabels.<label>`, `commonLabels.<label>`, `commonAnnotations.<name>`, `alerts.labels.<label>`, `alerts.annotations.<name>`, `alerts.status`, and so on. A path through the `alerts` list matches if **any** alert in the group matches. A path that doesn't exist never matches.

Labels are never top-level fields. `alertname = HighCPU` matches nothing; write `commonLabels.alertname = HighCPU`.

**pattern** is a case-insensitive wildcard that must match the **whole** value: `*` is any run of characters, `?` one character. No regular expressions, no negation. Without wildcards a pattern is an exact (case-insensitive) match, which is right for label values.

Which label field to use:

- `commonLabels.x` holds only labels that have the same value on every alert in the group. Good for `alertname`, `namespace`, `severity` when the group is homogeneous; missing when alerts disagree.
- `groupLabels.x` holds the labels Grafana's notification policy groups by; always present for those labels.
- `alerts.labels.x` matches if any alert in the group carries the value; the most forgiving choice.

`title` is Grafana's group title, such as `[FIRING:2] HighCPU infra`: a status prefix, then the group label values. It always needs wildcards: `title = *HighCPU*`. Prefer a label matcher when one exists; titles change with grouping.

### Examples

| Goal | Matchers |
|---|---|
| One alert rule, by name | `commonLabels.alertname = Container restarts` |
| Any alert whose name mentions restarts | `commonLabels.alertname = *restart*` |
| Critical alerts in prod namespaces, firing only | `commonLabels.severity = critical`<br>`commonLabels.namespace = prod-*`<br>`status = firing` |
| Any alert in the group from a given pod | `alerts.labels.pod = api-*` |
| Alerts from a Grafana folder | `commonLabels.grafana_folder = infra` |
| Everything else (put last) | *(no matchers)* |

A complete rule for the user to enter, as you would hand it over:

> **Name:** Container restarts
> **Matchers:**
> `commonLabels.alertname = Container restarts`
> **Template:** Container restarts
> **Destinations:** ops-teams
> Place it above the catch-all rule.

After saving a rule, the rule page's **Send test** button sends a chosen stored alert through it for real, and each alert's page shows which rule handled it.

## Templates

Write and check templates with `preview_template` against real stored alerts before `save_template`; rendering errors only show up against data. Updating a template changes live messages for every rule that uses it.

### Formatting

Template **bodies** are Slack mrkdwn for every destination; Teams destinations convert it to the same look. Use:

- bold: `*text*` (not `**text**`, which shows literally)
- italic: `_text_`
- strikethrough: `~text~`
- inline code: `` `text` ``
- code block: ```` ```text``` ````
- link: `<https://url|label>` or `<https://url>` (not `[label](https://url)`, which shows literally)

Emphasis markers only work at word boundaries, so `snake_case` values are safe. A single newline is a line break; a blank line separates paragraphs.

The **title** is plain text: each destination styles it itself (bold, a colored band, a link to Grafana), so don't put markup in it. Use Unicode emoji (🔴 ✅ ⚠️), not Slack `:shortcodes:`, which Teams shows literally.

Colors (the side bar in Slack attachments, the title band in Teams) come from the config file, red while firing and green once resolved by default; templates can't set them.

Keep bodies short: Slack collapses long attachments and Teams cards are cut at roughly 28 KB of card data. When a group can hold many alerts, list a summary line per alert rather than every label.
