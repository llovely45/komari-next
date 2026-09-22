FROM alpine:3.21

WORKDIR /app

# Docker buildx 会在构建时自动填充这些变量
ARG TARGETOS
ARG TARGETARCH

RUN apk add --no-cache ca-certificates curl redis tzdata

COPY --chmod=755 komari-${TARGETOS}-${TARGETARCH} /app/komari
COPY --chmod=755 docker-entrypoint.sh /app/docker-entrypoint.sh

ENV GIN_MODE=release
ENV KOMARI_LISTEN=0.0.0.0:25775

EXPOSE 25775

CMD ["/app/docker-entrypoint.sh"]
