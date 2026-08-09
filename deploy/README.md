# deploy/

## `seccomp-arcloom.json` — engine sandbox under Docker

ArcLoom contains the Terraform/OpenTofu child process with bubblewrap
(`internal/runner/sandbox.go`), because a user holding `resources`+`deploy` can
run arbitrary commands as the server through a `local-exec` provisioner. The
sandbox hides ArcLoom's own state — secrets, users, sessions, API keys, OAuth
grants, other projects — from that child.

Docker's **default seccomp profile blocks it**. The namespace and mount syscalls
bubblewrap needs are allowed only for a container holding `CAP_SYS_ADMIN`, and
`clone` is additionally filtered so that no new-namespace flag may be set. The
result is that the sandbox cannot start in the shipped image — as root or not.

This profile is Docker's default **plus one rule**, allowing exactly five
syscalls:

| syscall | why bubblewrap needs it |
|---|---|
| `unshare` | create the user + mount namespace |
| `clone` | same, via the namespace flags the default profile masks off |
| `mount` | set up the sandbox's own view (`--ro-bind`, `--tmpfs`, `--bind`) |
| `pivot_root` | swap to the prepared root |
| `umount2` | drop the old root afterwards |

Each was confirmed **necessary** by removing it and watching the sandbox fail,
and the set was confirmed **sufficient** by running a real `tofu` invocation
through it. `clone3` is deliberately *not* added: Docker's default already
answers it with `ENOSYS`, which glibc handles by falling back to `clone`.

Everything else in the profile is untouched, so the container keeps the rest of
its syscall filtering. That is the whole point of using this instead of
`seccomp:unconfined`, which switches filtering off entirely.

### Using it

`docker-compose.yml` references it already — uncomment:

```yaml
security_opt:
  - seccomp:./deploy/seccomp-arcloom.json
```

Plain Docker:

```sh
docker run --security-opt seccomp=./deploy/seccomp-arcloom.json ...
```

Confirm it worked — the startup log says which tier is active:

```
engine containment ready  sandbox=true tier=inherited-proc
```

`sandbox=false` means it is still blocked (the preceding warning says why).

### What you still do not get under Docker

`tier=inherited-proc`. Mounting a *private* `/proc` inside a user namespace
requires the kernel's "fully visible procfs" rule, and Docker deliberately masks
paths under `/proc` (`/proc/kcore`, `/proc/acpi`, …), which breaks it. ArcLoom
probes for this and falls back to the container's `/proc`, read-only.

The filesystem isolation is identical either way — this only means the sandboxed
process can still *see* the container's process list. Passing
`--security-opt systempaths=unconfined` would buy the private `/proc`, at the
cost of un-masking those `/proc` entries and making `/proc/sys` writable; that
is a worse trade than the process list is worth, so it is not recommended.

### Regenerating

The profile is a pinned copy of Docker's default and does not track upstream
automatically. If a Docker upgrade needs newer syscalls (a new glibc using a
syscall the pinned copy does not list), regenerate:

```sh
make seccomp-profile
```

which re-fetches moby's current default and re-applies the same rule.
