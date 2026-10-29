#!/usr/bin/env bash
# push reference client: sends a status to the push service.
#
# usage: push.sh CONFIG [STATUS_JSON]
#
#   CONFIG      file with a url=, name= and token= line each:
#                 url=https://push.example.com
#                 name=sensor-1
#                 token=<token>
#   STATUS_JSON optional file, its contents are sent as the status;
#               if omitted (or missing), {} is sent instead.
#
# Intended to be run from cron, e.g.:
#   * * * * * /usr/local/bin/push.sh /etc/push-client.conf /path/to/status.json
#
# Prints nothing on success; a failure is left to cron to mail.

set -eu

cfg=${1:?usage: push.sh CONFIG [STATUS_JSON]}
url=$(sed -n 's/^url=//p' "$cfg" | tail -n 1)
name=$(sed -n 's/^name=//p' "$cfg" | tail -n 1)
token=$(sed -n 's/^token=//p' "$cfg" | tail -n 1)
if [ -z "$url" ] || [ -z "$name" ] || [ -z "$token" ]; then
	echo "push.sh: $cfg needs url=, name= and token=" >&2
	exit 1
fi

if [ -n "${2:-}" ] && [ -f "$2" ]; then
	curl -fsS --data-binary @"$2" -H "X-Client-Token: $token" "$url/$name"
else
	curl -fsS --data-binary '{}' -H "X-Client-Token: $token" "$url/$name"
fi
