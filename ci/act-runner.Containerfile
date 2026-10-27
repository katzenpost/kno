FROM catthehacker/ubuntu:act-latest
RUN apt-get update && apt-get install -y --no-install-recommends \
      podman fuse-overlayfs uidmap iproute2 iptables slirp4netns shellcheck \
    && rm -rf /var/lib/apt/lists/*
RUN mkdir -p /var/lib/containers/storage /run/containers/storage && \
    printf '[storage]\ndriver = "vfs"\nrunroot = "/run/containers/storage"\ngraphroot = "/var/lib/containers/storage"\n' \
      > /etc/containers/storage.conf
RUN printf '[containers]\ndns_servers = ["9.9.9.9", "1.1.1.1"]\n' > /etc/containers/containers.conf
RUN useradd -m -u 1001 -s /bin/bash runner \
    && echo 'runner ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/runner \
    && printf 'runner:1:1000\nrunner:1002:64535\n' \
      | tee /etc/subuid > /etc/subgid
USER runner
ENV HOME=/home/runner USER=runner LOGNAME=runner
