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

ARG TERRAFORM_VERSION=1.9.8
ARG OPENTOFU_VERSION=1.8.8
ARG TARGETARCH=amd64

RUN set -eux; \
	apt-get update; \
	apt-get install -y --no-install-recommends ca-certificates curl unzip; \
	# Terraform
	curl -fsSL "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_${TARGETARCH}.zip" -o /tmp/tf.zip; \
	unzip /tmp/tf.zip -d /usr/local/bin; \
	# OpenTofu
	curl -fsSL "https://github.com/opentofu/opentofu/releases/download/v${OPENTOFU_VERSION}/tofu_${OPENTOFU_VERSION}_linux_${TARGETARCH}.zip" -o /tmp/tofu.zip; \
	unzip /tmp/tofu.zip tofu -d /usr/local/bin; \
	chmod +x /usr/local/bin/terraform /usr/local/bin/tofu; \
	rm -f /tmp/tf.zip /tmp/tofu.zip; \
	apt-get purge -y unzip; apt-get autoremove -y; rm -rf /var/lib/apt/lists/*; \
	terraform version; tofu version

COPY --from=gobuild /out/arcloom /usr/local/bin/arcloom

ENV ARCLOOM_ADDR=:8080 \
	ARCLOOM_ENGINE=tofu \
	ARCLOOM_DATA=/var/lib/arcloom/projects \
	ARCLOOM_SECRETS_DIR=/var/lib/arcloom/secrets \
	ARCLOOM_CACHE_DIR=/var/lib/arcloom/cache \
	TF_PLUGIN_CACHE_DIR=/var/lib/arcloom/cache/plugins \
	ARCLOOM_WORKDIR=/var/lib/arcloom/work
RUN mkdir -p /var/lib/arcloom
VOLUME ["/var/lib/arcloom"]

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/arcloom"]
