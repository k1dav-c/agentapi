FROM mcr.microsoft.com/devcontainers/go:1-1.24-bookworm

# The base image ships a yarn apt repo with an expired GPG key that breaks
# `apt-get update`; drop it before installing anything.
RUN rm -f /etc/apt/sources.list.d/yarn.list \
	&& apt-get update \
	&& apt-get install -y --no-install-recommends shellcheck \
	&& rm -rf /var/lib/apt/lists/*

# The image pins GOTOOLCHAIN=local but go.mod requires a newer patch release;
# let Go download the exact toolchain version on demand.
ENV GOTOOLCHAIN=auto

# proxy.golang.org is DNS-hijacked on this network (HiNet safebrowsing) and
# goproxy.io lacks toolchain modules; goproxy.cn mirrors both.
ENV GOPROXY=https://goproxy.cn,direct
