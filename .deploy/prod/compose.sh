#!/bin/sh
# Runs docker compose against the production stack in /opt/chatisimo. release.env names the
# image; the secrets come from the env_file paths in docker-compose.yaml.
# Usage (on the VM): sudo /opt/chatisimo/compose.sh <compose arguments>
set -eu

dir=/opt/chatisimo
exec docker compose -f "${dir}/docker-compose.yaml" --env-file "${dir}/release.env" "$@"
