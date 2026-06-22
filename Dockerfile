# syntax=docker/dockerfile:1
# check=error=true

FROM golang:alpine AS build
    WORKDIR /app

    RUN apk add --no-cache build-base

    RUN go install github.com/mailru/easyjson/easyjson@latest

    # RUN --mount=type=bind,source=togo/go.sum,target=go.sum \
    #     --mount=type=bind,source=togo/go.mod,target=go.mod \
    #     --mount=type=bind,source=togo/Makefile,target=Makefile \
    #     make tools

    COPY ./togo /app

    RUN make generate && make build

FROM scratch
    COPY --from=build /app/dist/cappu /cappu
    ENTRYPOINT ["/cappu"]
