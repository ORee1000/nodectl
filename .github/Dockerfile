FROM alpine:3.24.1

RUN apk --no-cache add ca-certificates su-exec tzdata \
    && addgroup -S -g 10001 nodectl \
    && adduser -S -D -H -u 10001 -G nodectl -s /sbin/nologin nodectl \
    && mkdir -p /app/data \
    && chown -R nodectl:nodectl /app

WORKDIR /app

ARG TARGETARCH

COPY --chmod=755 ./bin/nodectl-linux-${TARGETARCH} /app/nodectl
COPY --chmod=755 ./.github/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

ENV HOME=/app/data \
    TZ=Asia/Shanghai

VOLUME ["/app/data"]
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD wget -q -T 3 -O /dev/null http://127.0.0.1:8080/api/public/health \
        || wget --no-check-certificate -q -T 3 -O /dev/null https://127.0.0.1:8080/api/public/health

STOPSIGNAL SIGTERM

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/app/nodectl"]
