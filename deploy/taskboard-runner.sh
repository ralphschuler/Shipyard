#!/bin/sh
set -eu

binary=${TASKBOARD_BINARY:-/home/agent/taskboard/taskboard}
child=
restart_requested=0

stop_child() {
	if [ -n "$child" ]; then
		kill -TERM "$child" 2>/dev/null || true
	fi
}

trap 'restart_requested=1; stop_child' USR1
trap 'stop_child; exit 143' TERM INT HUP

# systemd supervises this process as MainPID. The application is deliberately
# a child so the detached update monitor can ask this runner to start the
# atomically installed candidate or restored previous bundle without creating
# a second supervisor or a competing MainPID.
while :; do
	restart_requested=0
	TASKBOARD_SUPERVISOR_PID=$$ "$binary" &
	child=$!
	set +e
	wait "$child"
	status=$?
	set -e
	child=
	if [ "$restart_requested" -eq 1 ]; then
		continue
	fi
	if [ "$status" -eq 0 ]; then
		exit 0
	fi
	sleep 1
done
