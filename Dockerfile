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
	apt-get install -y --no-install-recommends ca-certificates curl unzip; \
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
	terraform version; tofu version; tflint --version; conftest --version

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
RUN mkdir -p /var/lib/arcloom
VOLUME ["/var/lib/arcloom"]

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/arcloom"]
