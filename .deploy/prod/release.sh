#!/bin/sh
# Installs the stack files copied next to this script into /opt/chatisimo, pulls the release
# image given as $1 with the short-lived registry token read from stdin, loads present runtime
# settings from /config/chatisimo/prod/* into the database,
# starts the stack and waits until chatisimo-bot opens the Discord gateway. Run with sudo.
# The token lives only in a temporary Docker config removed on exit, and container logs stay on
# the VM: the CI log of a public repository is public.
set -eu

registry=us-east1-docker.pkg.dev
image="${1:-}"
case "${image}" in
  "${registry}"/*/chatisimo:*) ;;
  *)
    echo "release: expected ${registry}/<project>/<repository>/chatisimo:<tag>, got '${image}'" >&2
    exit 2
    ;;
esac

config=/config/chatisimo/prod
for file in postgres.env chatisimo.env; do
  if [ ! -f "${config}/${file}" ]; then
    echo "release: ${config}/${file} is missing; write it by hand first" >&2
    exit 3
  fi
done

token=""
IFS= read -r token || true
if [ -z "${token}" ]; then
  echo "release: no registry token on stdin" >&2
  exit 4
fi

src="$(dirname "$0")"
dir=/opt/chatisimo
install -m 0644 "${src}/docker-compose.yaml" "${dir}/docker-compose.yaml"
install -m 0755 "${src}/compose.sh" "${dir}/compose.sh"
printf 'CHATISIMO_IMAGE=%s\n' "${image}" > "${dir}/release.env"

auth_dir="$(mktemp -d)"
trap 'rm -rf "${auth_dir}"' EXIT
DOCKER_CONFIG="${auth_dir}"
export DOCKER_CONFIG
printf '%s' "${token}" | docker login -u oauth2accesstoken --password-stdin "https://${registry}" >/dev/null
token=""
"${dir}/compose.sh" pull --quiet
unset DOCKER_CONFIG

"${dir}/compose.sh" up -d --remove-orphans --wait chatisimo-postgres
# The files in /config/chatisimo/prod are the source of truth for runtime secrets and operator
# overrides: every release writes each present file to the settings table through stdin.
for key in discord.token llm.api_key llm.prompt uc.friday_alert_time uc.matchday_alert_time uc.slang_cache_ttl; do
  secret_file="${config}/${key}"
  if [ -f "${secret_file}" ]; then
    "${dir}/compose.sh" run --rm -T chatisimo-bot config set "${key}" < "${secret_file}" > /dev/null
    echo "release: loaded ${key} from ${secret_file}"
  fi
done

"${dir}/compose.sh" up -d --remove-orphans
"${dir}/compose.sh" up -d --no-deps --force-recreate chatisimo-bot

container="$("${dir}/compose.sh" ps -q chatisimo-bot)"
is_ready=no
for _ in $(seq 1 30); do
  if docker logs "${container}" 2>&1 | grep -q 'discord gateway open'; then
    is_ready=yes
    break
  fi
  sleep 2
done

state="$(docker inspect -f '{{.State.Running}} {{.RestartCount}}' "${container}")"
if [ "${is_ready}" != yes ] || [ "${state}" != "true 0" ]; then
  echo "release: chatisimo-bot is not healthy (running, restarts: ${state}); read 'sudo ${dir}/compose.sh logs chatisimo-bot' on the VM" >&2
  exit 1
fi

echo "release: ${image} is live"
