# The development container: a Linux with everything ghostwire and its tests
# need, so that the work can be done on a Mac, or on any machine with
# Docker. How to use it is in README.md next to this file; devcontainer.json
# gives the container the privileges ghostwire needs.
#
# Built from the repository root (devcontainer.json sets the context), so
# that the Python requirements of the tests can be installed here.

# Pinned, like every dependency, to a release more than two weeks old.
FROM golang:1.27.0-bookworm

# What ghostwire uses at run time (the TUN device), what the end-to-end
# and compatibility checks run (iproute2, ping, curl, traceroute, mtr,
# fping, Python), and what make lint needs (shellcheck). sudo is for the
# checks that need root; the rest of the time the container runs as the
# unprivileged user dev, like ghostwire exec's users.
RUN apt-get update -q && apt-get install -y -q --no-install-recommends \
      iproute2 iputils-ping curl ca-certificates traceroute mtr-tiny fping nscd \
      python3 python3-pip python3-venv shellcheck \
      sudo git less procps vim-tiny nano \
    && rm -rf /var/lib/apt/lists/*

# Login shells, which VS Code and the devcontainer CLI use, get their PATH
# from /etc/profile, which does not know where the Go image put Go.
RUN echo 'export PATH="$PATH:/usr/local/go/bin:/go/bin"' > /etc/profile.d/go.sh

# The Go module and build caches live in volumes (see devcontainer.json),
# so that builds stay incremental across containers; the directories are
# created here so that the volumes get the user's ownership.
ARG USERNAME=dev
ARG UID=1000
RUN useradd -m -u "$UID" -s /bin/bash "$USERNAME" \
    && echo "$USERNAME ALL=(ALL) NOPASSWD:ALL" > "/etc/sudoers.d/$USERNAME" \
    && chmod 0440 "/etc/sudoers.d/$USERNAME" \
    && mkdir -p /go/pkg/mod "/home/$USERNAME/.cache" \
    && chown -R "$USERNAME" /go "/home/$USERNAME/.cache"

# The Python libraries of the compatibility checks (icmplib) and of the
# Python example (pytest, requests). The container has no other Python, so
# installing them system-wide is fine.
COPY test/compat/requirements.txt /tmp/compat-requirements.txt
COPY examples/python-demo/requirements.txt /tmp/python-demo-requirements.txt
RUN pip install --no-cache-dir --break-system-packages \
      -r /tmp/compat-requirements.txt -r /tmp/python-demo-requirements.txt \
    && rm /tmp/compat-requirements.txt /tmp/python-demo-requirements.txt

# The shell history survives rebuilds in a volume (see devcontainer.json).
RUN mkdir /commandhistory && touch /commandhistory/.bash_history \
    && chown -R "$USERNAME" /commandhistory \
    && echo "export PROMPT_COMMAND='history -a' HISTFILE=/commandhistory/.bash_history" >> "/home/$USERNAME/.bashrc"

ENV DEVCONTAINER=true
USER $USERNAME
WORKDIR /workspace
RUN git config --global --add safe.directory /workspace

# Claude Code, to work in the container as on the host. Its configuration
# lives in a volume (see devcontainer.json), so a login survives rebuilds.
ARG CLAUDE_CODE_VERSION=stable
RUN mkdir -p "/home/$USERNAME/.claude" \
    && curl -fsSL https://claude.ai/install.sh | bash -s "$CLAUDE_CODE_VERSION"
ENV PATH="/home/$USERNAME/.local/bin:$PATH" CLAUDE_CONFIG_DIR="/home/$USERNAME/.claude"
