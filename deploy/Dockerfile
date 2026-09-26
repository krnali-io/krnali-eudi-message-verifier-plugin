FROM golang:1.26.4-bookworm@sha256:b305420a68d0f229d91eb3b3ed9e519fcf2cf5461da4bef997bf927e8c0bfd2b AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /verifylink ./cmd/verifylink

FROM scratch
COPY THIRD_PARTY_NOTICES.md /THIRD_PARTY_NOTICES.md
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /verifylink /verifylink
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/verifylink"]
