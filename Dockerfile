# syntax=docker/dockerfile:1
# check=error=true

FROM node:26-slim AS build
    WORKDIR /app

    RUN --mount=type=bind,source=package.json,target=package.json \
        --mount=type=bind,source=package-lock.json,target=package-lock.json \
        --mount=type=cache,target=/root/.npm \
        npm ci --no-audit

    COPY ./ /app

    # Only the self-contained CLI bundle is needed - node runs it directly, so
    # there is no point baking a Node SEA binary into a node image (and SEA does
    # not support Alpine/musl anyway). CAPPU_SKIP_EXE skips the cross-compiled
    # binaries; noExternal makes dist/cli.mjs carry its dependencies.
    RUN CAPPU_SKIP_EXE=1 node --run build

FROM node:26-slim
    COPY --from=build /app/dist/cli.mjs /cappu.mjs
    ENTRYPOINT ["node", "/cappu.mjs"]
