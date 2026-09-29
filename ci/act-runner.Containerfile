FROM catthehacker/ubuntu:act-latest
RUN apt-get update && apt-get install -y --no-install-recommends \
      podman fuse-overlayfs uidmap iproute2 iptables slirp4netns shellcheck \
    && rm -rf /var/lib/apt/lists/*
RUN mkdir -p /var/lib/containers/storage /run/containers/storage && \
    printf '[storage]\ndriver = "vfs"\nrunroot = "/run/containers/storage"\ngraphroot = "/var/lib/containers/storage"\n' \
      > /etc/containers/storage.conf
RUN printf '[containers]\ndns_servers = ["9.9.9.9", "1.1.1.1"]\n' > /etc/containers/containers.conf
