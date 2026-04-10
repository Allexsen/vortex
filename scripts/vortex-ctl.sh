#!/usr/bin/env bash
set -euo pipefail

REDIS_CLI="${REDIS_CLI:-docker compose exec -T state_store redis-cli}"

CRAWLER_KEY="vortex:control:crawler"
EMBEDDER_KEY="vortex:control:embedder"

usage() {
    cat <<EOF
Usage: $0 <service> <command>

Services:  crawler | embedder
Commands:  pause | resume | auto | status

Examples:
  $0 crawler pause      Pause all crawlers
  $0 crawler resume     Resume crawlers (manual)
  $0 crawler auto       Return to auto-throttle mode
  $0 crawler status     Show current crawler state
  $0 embedder pause     Pause the embedder
  $0 embedder resume    Resume the embedder
  $0 embedder status    Show current embedder state

Environment:
  REDIS_CLI   Override redis-cli command (default: docker compose exec -T state_store redis-cli)
EOF
}

run_command() {
    local key="$1"
    local service="$2"
    local cmd="$3"

    case "$cmd" in
        pause)
            $REDIS_CLI SET "$key" pause > /dev/null
            echo "$service: paused"
            ;;
        resume)
            $REDIS_CLI SET "$key" resume > /dev/null
            echo "$service: resumed (manual)"
            ;;
        auto)
            $REDIS_CLI DEL "$key" > /dev/null
            echo "$service: auto mode"
            ;;
        status)
            val=$($REDIS_CLI GET "$key")
            if [ -z "$val" ]; then
                echo "$service: auto mode (no manual override)"
            else
                echo "$service: $val (manual override)"
            fi
            ;;
        *)
            echo "Unknown command: $cmd" >&2
            usage
            exit 1
            ;;
    esac
}

SERVICE="${1:-}"
COMMAND="${2:-}"

if [ -z "$SERVICE" ] || [ -z "$COMMAND" ]; then
    usage
    exit 1
fi

case "$SERVICE" in
    crawler)  run_command "$CRAWLER_KEY" "crawler" "$COMMAND" ;;
    embedder) run_command "$EMBEDDER_KEY" "embedder" "$COMMAND" ;;
    *)
        echo "Unknown service: $SERVICE" >&2
        usage
        exit 1
        ;;
esac
