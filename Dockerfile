FROM node:26-bookworm AS web
WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm ci
COPY web/ ./
RUN npm run build   # -> /web/build (adapter-static SPA, 200.html fallback)

FROM golang:1.26-bookworm AS gobuild
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN rm -rf internal/webui/dist && mkdir -p internal/webui/dist
COPY --from=web /web/build/ internal/webui/dist/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/arcloom ./cmd/arcloom

FROM debian:bookworm-slim AS runtime

ARG TERRAFORM_VERSION=1.15.8
ARG OPENTOFU_VERSION=1.12.4
ARG TFLINT_VERSION=0.64.0
ARG CONFTEST_VERSION=0.68.2
ARG TARGETARCH=amd64

RUN set -eux; \
	apt-get update; \
	# bubblewrap contains the engine child process: a `local-exec` provisioner or
	# a provider plugin runs as this process, so the sandbox is what keeps it away
	# from /var/lib/arcloom (secrets, users, sessions, keys, other projects).
	# ArcLoom probes it at start-up and degrades to the environment scrub alone if
	# the runtime forbids unprivileged user namespaces — see internal/runner/sandbox.go.
	apt-get install -y --no-install-recommends ca-certificates curl unzip bubblewrap; \
	# conftest release archives use uname-style arch names
	case "${TARGETARCH}" in amd64) CONFTEST_ARCH=x86_64 ;; *) CONFTEST_ARCH="${TARGETARCH}" ;; esac; \
	# Terraform
	curl -fsSL "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_${TARGETARCH}.zip" -o /tmp/tf.zip; \
	unzip /tmp/tf.zip -d /usr/local/bin; \
	# OpenTofu
	curl -fsSL "https://github.com/opentofu/opentofu/releases/download/v${OPENTOFU_VERSION}/tofu_${OPENTOFU_VERSION}_linux_${TARGETARCH}.zip" -o /tmp/tofu.zip; \
	unzip /tmp/tofu.zip tofu -d /usr/local/bin; \
	# tflint (optional; enables the Lint feature — degrades gracefully if absent)
	curl -fsSL "https://github.com/terraform-linters/tflint/releases/download/v${TFLINT_VERSION}/tflint_linux_${TARGETARCH}.zip" -o /tmp/tflint.zip; \
	unzip /tmp/tflint.zip tflint -d /usr/local/bin; \
	# conftest (optional; enables the policy-as-code gate — degrades gracefully if
	# absent, except that an ENFORCING project then blocks applies by design)
	curl -fsSL "https://github.com/open-policy-agent/conftest/releases/download/v${CONFTEST_VERSION}/conftest_${CONFTEST_VERSION}_Linux_${CONFTEST_ARCH}.tar.gz" -o /tmp/conftest.tgz; \
	tar -xzf /tmp/conftest.tgz -C /usr/local/bin conftest; \
	chmod +x /usr/local/bin/terraform /usr/local/bin/tofu /usr/local/bin/tflint /usr/local/bin/conftest; \
	rm -f /tmp/tf.zip /tmp/tofu.zip /tmp/tflint.zip /tmp/conftest.tgz; \
	apt-get purge -y unzip; apt-get autoremove -y; rm -rf /var/lib/apt/lists/*; \
	terraform version; tofu version; tflint --version; conftest --version; bwrap --version

COPY --from=gobuild /out/arcloom /usr/local/bin/arcloom

ENV ARCLOOM_ADDR=:8080 \
	ARCLOOM_ENGINE=tofu \
	ARCLOOM_DATA=/var/lib/arcloom/projects \
	ARCLOOM_SECRETS_DIR=/var/lib/arcloom/secrets \
	ARCLOOM_USERS_DIR=/var/lib/arcloom/users \
	ARCLOOM_SESSIONS_DIR=/var/lib/arcloom/sessions \
	ARCLOOM_CACHE_DIR=/var/lib/arcloom/cache \
	TF_PLUGIN_CACHE_DIR=/var/lib/arcloom/cache/plugins \
	ARCLOOM_WORKDIR=/var/lib/arcloom/work
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

# The server runs as a normal account, not root. It executes provider plugins
# and `local-exec` provisioners (see internal/runner/sandbox.go), so uid 0 is the
# wrong thing for that code to inherit if it ever escapes the sandbox — or on a
# host where the sandbox cannot start at all.
#
# The entrypoint still STARTS as root so it can adopt a volume created by an
# older, root-running version; it hands ownership to this user and drops
# privileges before exec'ing the server. A plain `USER` directive would leave
# every existing install crash-looping on a data dir it cannot write.
RUN useradd --system --uid 10001 --home-dir /var/lib/arcloom --shell /usr/sbin/nologin arcloom; \
	mkdir -p /var/lib/arcloom; \
	chown -R arcloom:arcloom /var/lib/arcloom; \
	chmod 0700 /var/lib/arcloom; \
	chmod +x /usr/local/bin/docker-entrypoint.sh
VOLUME ["/var/lib/arcloom"]

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD []
