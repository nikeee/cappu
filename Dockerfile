# syntax=docker/dockerfile:1
# check=error=true

FROM node:alpine AS build
    WORKDIR /app

    RUN --mount=type=bind,source=package.json,target=package.json \
        --mount=type=bind,source=package-lock.json,target=package-lock.json \
        --mount=type=cache,target=/root/.npm \
        npm ci --no-audit

    COPY ./ /app

    RUN node --run build:native

FROM alpine:latest
    RUN apk add --no-cache libstdc++
    COPY --from=build /app/dist/cappu /cappu

    CMD ["/cappu"]
