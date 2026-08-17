# Mullvad-tunnelled CONNECT proxy. Built and deployed separately from tclaw:
#   fly deploy -c egress/fly.toml --dockerfile egress/Dockerfile
FROM golang:1.25-alpine AS build

# wireguard-go is the WireGuard project's own userspace implementation. It is
# built from source and pinned because Alpine dropped the package, and because
# wg-quick needs a userspace fallback wherever the host kernel has no WireGuard
# module — which is not something we can rely on from inside a Fly machine.
ARG WIREGUARD_GO_VERSION=v0.0.0-20260522210424-ecfc5a8d5446
# The module's main package sits at its root, so the binary lands as
# "wireguard"; wg-quick looks it up by the name in
# WG_QUICK_USERSPACE_IMPLEMENTATION, hence the rename.
RUN go install golang.zx2c4.com/wireguard@${WIREGUARD_GO_VERSION} \
	&& mv /go/bin/wireguard /go/bin/wireguard-go

WORKDIR /src
COPY egress/main.go ./
# The proxy itself has no dependencies beyond the standard library — keep it
# that way, so the process holding the VPN credentials stays easy to audit.
RUN go mod init egress && CGO_ENABLED=0 go build -o /out/egress .

FROM alpine:3.21

RUN apk add --no-cache \
	wireguard-tools \
	iproute2 \
	iptables \
	curl \
	ca-certificates

COPY --from=build /out/egress /usr/local/bin/egress
COPY --from=build /go/bin/wireguard-go /usr/local/bin/wireguard-go
COPY egress/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

# Fail at build time rather than discovering a missing tool mid-deploy. Every
# one of these is load-bearing at boot.
RUN test -x /usr/local/bin/egress \
	&& test -x /usr/local/bin/entrypoint.sh \
	&& test -x /usr/local/bin/wireguard-go \
	&& command -v wg-quick \
	&& command -v ip \
	&& command -v curl

EXPOSE 8000
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
