# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM docker.m.daocloud.io/library/golang:1.26.3-bookworm AS build
ARG TARGETOS=linux
ARG TARGETARCH
ENV GOTOOLCHAIN=local \
    GOPROXY=https://goproxy.cn,direct \
    GOSUMDB=sum.golang.google.cn \
    CGO_ENABLED=0
WORKDIR /src
COPY . .
RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -mod=vendor -trimpath -o /out/conshash .

FROM docker.m.daocloud.io/library/alpine:3.20
COPY --from=build /out/conshash /app
ENTRYPOINT ["/app"]
