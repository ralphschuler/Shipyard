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
	# Keep the systemd MainPID alive even when the child exits cleanly. The
	# update monitor may still need to signal this runner after terminating a
	# failed candidate, before it has restored and verified the previous bundle.
	# Only an explicit TERM/INT/HUP trap exits the supervisor.
	sleep 1
done
