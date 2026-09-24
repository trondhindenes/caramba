# caramba

A webhook receiver for [Grafana alerts](https://grafana.com/docs/grafana/latest/alerting/). caramba receives and persists alert notifications so you can browse them in a web GUI, author message templates against real payloads, and forward templated messages to destinations such as Slack.

**Status: early prototype.** Receiving, persisting, and browsing alerts works. Templating and forwarding are under development.

## How it works

Grafana sends one webhook delivery per alert group. caramba stores each delivery verbatim as a JSON file in a pluggable blob store (local folder today, GCS bucket planned) and serves a server-rendered GUI for browsing them. Planned next: templates in both Grafana's notification language and Jinja2, plus rule-based forwarding (label matchers → template → destination).

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
destinations: []                  # Slack destinations, used by forwarding (not implemented yet)
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

### AI agents (MCP)

When `mcp_token` is set, caramba serves a [Model Context Protocol](https://modelcontextprotocol.io) endpoint at `/mcp` (streamable HTTP, stateless). It lets an agent browse stored alerts and iterate on templates by previewing drafts against real payloads. The tools are read-only — `list_alerts`, `get_alert`, `list_templates`, `get_template`, `preview_template` — so the agent can't save templates or send anything.

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
internal/mcpserver/ MCP endpoint for AI agents (read-only tools)
testdata/          captured Grafana webhook payloads used as fixtures
```

Storage is deliberately simple for now — plain JSON files, one per webhook delivery, with keys that sort chronologically (`alerts/<timestamp>-<ulid>.json`). All reads and writes go through the repositories in `internal/repo`, so the planned move to an indexed database won't ripple beyond that package.
