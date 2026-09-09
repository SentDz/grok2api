FROM node:22-alpine

RUN apk add --no-cache python3 && \
    addgroup -S -g 10001 signer && \
    adduser -S -D -H -u 10001 -G signer signer

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

WORKDIR /app
COPY --chown=signer:signer statsig_signer ./statsig_signer
COPY --chown=signer:signer hot ./hot
COPY --chown=signer:signer data ./data

USER signer
EXPOSE 8788

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD python3 -c 'import json, urllib.request; r = json.load(urllib.request.urlopen("http://127.0.0.1:8788/health", timeout=3)); assert r["ok"] and r["path_count"] == 4'

CMD ["python3", "-m", "statsig_signer", "serve", "--listen", "0.0.0.0:8788"]
