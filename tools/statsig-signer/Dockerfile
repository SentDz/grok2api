FROM node:22-bookworm-slim AS node-runtime

FROM python:3.11-slim-bookworm

COPY --from=node-runtime /usr/local/bin/node /usr/local/bin/node

RUN sed -i 's|http://deb.debian.org|https://deb.debian.org|g' /etc/apt/sources.list.d/debian.sources && \
    apt-get -o Acquire::Retries=3 update && \
    apt-get -o Acquire::Retries=3 install -y --no-install-recommends chromium ca-certificates && \
    rm -rf /var/lib/apt/lists/* && \
    python3 -m venv /opt/venv && \
    /opt/venv/bin/pip install --no-cache-dir playwright==1.58.0 && \
    groupadd -g 10001 signer && \
    useradd -m -u 10001 -g signer signer && \
    mkdir -p /var/lib/statsig && chown signer:signer /var/lib/statsig

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PATH="/opt/venv/bin:$PATH" \
    PLAYWRIGHT_CHROMIUM=/usr/bin/chromium

WORKDIR /app
COPY --chown=signer:signer statsig_signer ./statsig_signer
COPY --chown=signer:signer hot ./hot
COPY --chown=signer:signer data ./data

USER signer
EXPOSE 8788

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD python3 -c 'import json, urllib.request; r = json.load(urllib.request.urlopen("http://127.0.0.1:8788/health", timeout=3)); assert r["ok"] and r["path_count"] == 4'

CMD ["python3", "-m", "statsig_signer", "serve", "--listen", "0.0.0.0:8788"]
