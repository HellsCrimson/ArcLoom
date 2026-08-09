#!/bin/sh
# ArcLoom container entrypoint.
#
# The server must not run as root: it executes provider plugins and, for anyone
# holding `resources`+`deploy`, `local-exec` provisioners. The sandbox
# (internal/runner/sandbox.go) keeps those away from ArcLoom's own data, but uid
# 0 is still the wrong identity for them to inherit on a host where the sandbox
# cannot start.
#
# Dropping privileges here rather than with a plain `USER` directive is what
# makes an upgrade work: a volume created by an older, root-running ArcLoom is
# owned by root, and a container that simply started as an unprivileged user
# would crash-loop on a data directory it cannot write.
set -eu

ARCLOOM_HOME=/var/lib/arcloom
ARCLOOM_USER=arcloom

if [ "$(id -u)" = "0" ]; then
	# Adopt the data directory if it is not already ours. The ownership test
	# comes first because a recursive chown of a large volume on every start
	# would be slow for no reason — the common case is "already correct".
	if [ -d "$ARCLOOM_HOME" ] && [ "$(stat -c %u "$ARCLOOM_HOME")" != "$(id -u "$ARCLOOM_USER")" ]; then
		echo "arcloom: adopting $ARCLOOM_HOME for user $ARCLOOM_USER (one-time migration)" >&2
		chown -R "$ARCLOOM_USER:$ARCLOOM_USER" "$ARCLOOM_HOME"
	fi
	chmod 0700 "$ARCLOOM_HOME" 2>/dev/null || true

	# setpriv ships with util-linux, which is already in the base image — one
	# less package than gosu, and --init-groups gives the process its
	# supplementary groups rather than only its primary one.
	exec setpriv --reuid="$ARCLOOM_USER" --regid="$ARCLOOM_USER" --init-groups \
		/usr/local/bin/arcloom "$@"
fi

# Already unprivileged: the operator set `user:` in compose, or the runtime
# assigned a uid. Respect that and start directly — re-dropping is neither
# possible nor wanted.
exec /usr/local/bin/arcloom "$@"
