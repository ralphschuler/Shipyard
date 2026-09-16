#!/usr/bin/env bash
# Starts the local Shipyard server behind the trusted Nginx proxy.  The proxy
# identity values are intentionally read only at process start; they are never
# written to the database, command line, or logs.
set -euo pipefail

cd /home/agent/taskboard
export DATABASE_URL='postgres://taskboard:taskboard@localhost:5432/taskboard?sslmode=disable'
export TASKBOARD_ADDR='127.0.0.1:8080'
export TASKBOARD_PROXY_SSO_EMAIL="$(sudo -n awk -F'"' '/X-Taskboard-Proxy-Email/{print $2}' /etc/nginx/snippets/taskboard-sso.conf)"
export TASKBOARD_PROXY_SSO_SECRET="$(sudo -n awk -F'"' '/X-Taskboard-Proxy-Secret/{print $2}' /etc/nginx/snippets/taskboard-sso.conf)"

exec /home/agent/taskboard/taskboard
