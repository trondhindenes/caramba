# caramba

A webhook receiver for [Grafana alerts](https://grafana.com/docs/grafana/latest/alerting/). caramba receives and persists alert notifications so you can browse them in a web GUI, author message templates against real payloads, and forward templated messages to destinations such as Slack.

**Status: early prototype.** Receiving, persisting, browsing, templating, and routing alerts to Slack work.

## How it works

Grafana sends one webhook delivery per alert group. caramba stores each delivery verbatim as a JSON file in a pluggable blob store (local folder today, GCS bucket planned) and serves a server-rendered GUI for browsing them. Message templates can be written in Grafana's notification language or Jinja2, and routing rules forward each received alert to destinations such as Slack.

## Building

Requires Go 1.26+.

```sh
go build -o caramba ./cmd/server
```

This produces a single self-contained binary — GUI assets are embedded.

### Docker

CI builds and pushes an image to GitHub Container Registry on every push, tagged with a semver calculated by [autoversion-action](https://github.com/trondhindenes/autoversion-action) (plus `latest` on releases). The container expects its config at `/etc/caramba/config.yml`:

```sh
docker run -p 8080:8080 \
  -v $(pwd)/config.yml:/etc/caramba/config.yml \
  -v caramba-data:/data \
  -e WEBHOOK_TOKEN=some-secret \
  ghcr.io/trondhindenes/caramba:latest
```

Point `store.path` at a mounted volume (e.g. `/data`) so alerts survive container restarts.

## Running

```sh
cp config.example.yml config.yml   # edit to taste
export WEBHOOK_TOKEN=some-secret
./caramba --config config.yml
```

The GUI is served at `http://localhost:8080/`. There is a `/healthz` endpoint for liveness checks.

### Configuration

Config values support `${ENV_VAR}` expansion so secrets stay out of the file:

```yaml
listen: ":8080"
webhook_token: ${WEBHOOK_TOKEN}   # shared secret for the webhook endpoint (required)
mcp_token: ${MCP_TOKEN}           # bearer token for the MCP endpoint; empty disables it
retention: 720h                   # how long to keep alerts; 0 = forever (cleanup job not implemented yet)
store:
  type: local                     # local | gcs (gcs not implemented yet)
  path: ./data
destinations:                     # where routing rules can send messages
  - name: ops-slack
    type: slack-attachment          # slack (plain text) | slack-attachment (color bar)
    webhook_url: ${SLACK_WEBHOOK_OPS}
colors: []                        # optional title → color rules for slack-attachment
```

### Pointing Grafana at it

Create a **webhook contact point** in Grafana alerting:

- URL: `http://<your-host>:8080/webhook`
- HTTP method: `POST`
- Authorization header scheme: `Bearer`, credentials: the value of `webhook_token`

Fire a test notification from Grafana and the alert appears on the GUI front page.

Or simulate one from the command line:

```sh
curl -X POST localhost:8080/webhook \
  -H "Authorization: Bearer $WEBHOOK_TOKEN" \
  --data @testdata/grafana-payload.json
```

### Setting up a Slack destination

caramba posts to Slack through [incoming webhooks](https://api.slack.com/messaging/webhooks). Each webhook URL is tied to one channel, so create one per channel you want to route to:

1. Go to [api.slack.com/apps](https://api.slack.com/apps) → **Create New App** → **From scratch**, name it (e.g. `caramba`) and pick your workspace.
2. Under **Incoming Webhooks**, switch **Activate Incoming Webhooks** on.
3. Click **Add New Webhook to Workspace**, choose the channel (e.g. `#ops-alerts`) and allow it.
4. Copy the webhook URL (`https://hooks.slack.com/services/T…/B…/…`). Treat it as a secret: anyone with it can post to the channel.

Reference it from the config through an environment variable, so the URL stays out of the file. There are two Slack destination types:

- `slack` — a plain message: the template title in bold, then the body.
- `slack-attachment` — a (legacy) Slack attachment, like Grafana's own Slack notifications: a colored bar down the side, the title linking to the alert in Grafana, and a footer. Slack collapses long attachment text behind "Show more".

```yaml
destinations:
  - name: ops-slack                         # the name rules refer to
    type: slack-attachment
    webhook_url: ${SLACK_WEBHOOK_OPS}
  - name: dev-slack
    type: slack
    webhook_url: ${SLACK_WEBHOOK_DEV}

# Optional colors for slack-attachment destinations. The first rule whose
# pattern matches the alert's Grafana title (e.g. "[FIRING:1] disk critical …")
# wins; unmatched alerts are red while firing and green once resolved.
colors:
  - title: "*critical*"
    color: "#D63232"                        # #RRGGBB, or Slack's good / warning / danger
  - title: "*(staging)*"
    color: warning
```

```sh
docker run -p 8080:8080 \
  -v $(pwd)/config.yml:/etc/caramba/config.yml \
  -v caramba-data:/data \
  -e WEBHOOK_TOKEN=some-secret \
  -e SLACK_WEBHOOK_OPS=https://hooks.slack.com/services/T000/B000/XXXX \
  -e SLACK_WEBHOOK_DEV=https://hooks.slack.com/services/T000/B111/YYYY \
  ghcr.io/trondhindenes/caramba:latest
```

Destinations and colors are read at startup; restart caramba after changing them. Destinations then appear as checkboxes on the rule editor. Message bodies are Slack mrkdwn, so preview templates with **Render as: slack** to see what the channel will get, and use a rule's **Send test** button to check the webhook end to end.

### Routing

Routing rules (the **Rules** page in the GUI) decide where each received alert goes. Rules are evaluated top to bottom and the **first match wins**. A rule combines:

- **Matchers**, one per line as `field = pattern`, all of which must match. `field` is `title` or a dotted path into the alert JSON (`commonLabels.namespace`); paths through lists match if any item matches (`alerts.labels.pod`). Patterns are case-insensitive wildcards (`*`, `?`).
- A **template** that renders the message.
- One or more **destinations** from the config file. A rule with none drops matching alerts.

A rule without matchers matches everything; put one last as the default:

| # | Matchers | Template | Destinations |
|---|---|---|---|
| 1 | `title = *container restarts*` | restarts | dev-slack |
| 2 | *(none)* | default | ops-slack |

Routing runs in the background after an alert is stored. Transient Slack failures (5xx, 429, network) are retried; the outcome is shown on the alert's page. Each rule page has a **Send test** button that sends a stored alert through the rule for real.

### AI agents (MCP)

When `mcp_token` is set, caramba serves a [Model Context Protocol](https://modelcontextprotocol.io) endpoint at `/mcp` (streamable HTTP, stateless). It lets an agent browse stored alerts and author templates by previewing drafts against real payloads:

- read: `list_alerts`, `get_alert`, `list_templates`, `get_template`, `preview_template`
- write: `save_template` (create, or update by id; validated before saving) and `delete_template` (refused while a routing rule uses the template)

The agent can't send messages or change routing rules. Note that updating a template changes live messages for every rule using it.

Add it to Claude Code:

```sh
claude mcp add --transport http caramba http://localhost:8080/mcp \
  --header "Authorization: Bearer $MCP_TOKEN"
```

Alert labels and annotations come from monitored systems and are passed to the agent as-is; treat them as untrusted input.

## Development

```sh
go test ./...   # run all tests
go vet ./...
```

Project layout:

```
cmd/server/        entrypoint
internal/config/   YAML config loading and validation
internal/model/    Grafana webhook payload structs
internal/store/    blob store interface + local-folder implementation
internal/repo/     typed repositories on top of the store (the future-DB seam)
internal/web/      webhook endpoint + server-rendered GUI (embedded templates/CSS)
internal/mcpserver/ MCP endpoint for AI agents (alerts, templates)
internal/route/    rule matching (first match wins)
internal/dispatch/ routes received alerts: match, render, send, record
internal/notify/   destination senders (Slack plain text and attachments)
testdata/          captured Grafana webhook payloads used as fixtures
```

Storage is deliberately simple for now — plain JSON files, one per webhook delivery, with keys that sort chronologically (`alerts/<timestamp>-<ulid>.json`). All reads and writes go through the repositories in `internal/repo`, so the planned move to an indexed database won't ripple beyond that package.
